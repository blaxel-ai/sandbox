package archive

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/blaxel-ai/sandbox-api/src/handler/process"
	"github.com/blaxel-ai/sandbox-api/src/lib/blaxel"
	"github.com/sirupsen/logrus"
)

const (
	// EnvImportURL holds a presigned GET URL of an archive to restore. It is
	// read once, at boot, before the workload starts.
	EnvImportURL = "BL_ARCHIVE_IMPORT_URL"
	// markerTemporarySuffix names the file the marker is written to before it is
	// renamed into place. It is excluded from an archive like the marker itself,
	// since an archive that could create it would decide what this API's own
	// root-privileged write opens.
	markerTemporarySuffix = ".tmp"
	// DefaultImportMarker records the archive this sandbox restored.
	//
	// It lives on the archived filesystem on purpose, and its lifetime is what
	// makes an import happen exactly once. The upper layer is a tmpfs, so:
	//   - sandbox-api restarting (a crash, an OOM kill, a hot upgrade) finds the
	//     marker and the restored files still there, and does not import again,
	//     which would undo whatever the workload has done since;
	//   - the VM booting again starts from the pristine image with neither the
	//     marker nor the restored files, and imports, which is the only way the
	//     sandbox comes back to the state the archive describes.
	DefaultImportMarker = "/var/lib/blaxel/archive-import.json"
)

// ErrNoImport is returned when there is nothing to import, so a caller can tell
// it from a failed import.
var ErrNoImport = errors.New("no archive to import")

// ErrPartialImport reports an import that failed after it had already written to
// the filesystem, which is the one failure a sandbox must not simply boot
// through: the files are a mix of the image's and the archive's, and a workload
// running on them would build on a state that never existed.
var ErrPartialImport = errors.New("the archive was partially restored")

// maxMetadataBytes bounds the archive members that are read into memory rather
// than written to the filesystem - the manifest and the process list. An archive
// is untrusted data read from a URL the caller chose, and these two are the only
// members whose size a reader has to hold: a crafted one would otherwise have
// the boot allocate until the API is killed. The bound is far above what a real
// archive carries, where both are lists of paths and commands.
const maxMetadataBytes = 32 << 20

// readMetadata reads an archive member that is held in memory, refusing one
// larger than a description of a filesystem has any reason to be.
func readMetadata(reader io.Reader, name string) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(reader, maxMetadataBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read %s from the archive: %w", name, err)
	}
	if len(content) > maxMetadataBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes, which no archive of a filesystem is", name, maxMetadataBytes)
	}
	return content, nil
}

// ImportOptions drives an import.
type ImportOptions struct {
	// URL is a presigned S3 GET URL of the archive.
	URL string `json:"url,omitempty"`
	// RelaunchProcesses starts the processes the archive recorded as running.
	// Defaults to true; they are relaunched from their command, never adopted by
	// the PID they had in the archived sandbox.
	RelaunchProcesses *bool `json:"relaunchProcesses,omitempty"`
	// Excludes are added to the paths never restored.
	Excludes []string `json:"excludes,omitempty"`
	// MarkerPath overrides where the import is recorded.
	MarkerPath string `json:"markerPath,omitempty"`

	// root is only set by tests, which restore into a plain directory.
	root string
	// onRestored runs once the filesystem carries the archive and before
	// anything is relaunched from it. It is how the import is recorded as done
	// before it has any visible effect: a crash between the two would otherwise
	// leave no record, and the next start would import the archive a second time
	// over a filesystem already running the first one. It is handed the process
	// list still to be relaunched, so the record says the filesystem is restored
	// and the workload is not started yet.
	onRestored func(result *ImportResult, pendingProcesses []byte) error
	// onWriting runs once, just before the first member is written. Until it
	// has run the filesystem is still the image's, which is what tells a start
	// following a killed import whether it has a mix to deal with or an attempt
	// that never got past its download.
	onWriting func()
}

// ImportResult reports an import.
type ImportResult struct {
	Manifest Manifest `json:"manifest"`
	// Restored is the number of paths written, Deleted the number removed.
	Restored int `json:"restored"`
	Deleted  int `json:"deleted"`
	// Skipped are the archive members left out because they are excluded.
	Skipped []string `json:"skipped,omitempty"`
	// Relaunched are the identifiers of the newly started processes. They are
	// new: the archived sandbox's PIDs mean nothing here.
	Relaunched []string `json:"relaunched,omitempty"`
	// FailedRelaunches names the archived processes that could not be started
	// again. The filesystem is the archive's, so the import itself succeeded, but
	// part of the workload is missing and that has to be visible rather than
	// living in a log line nobody reads.
	FailedRelaunches []string `json:"failedRelaunches,omitempty"`
	Bytes            int64    `json:"bytes"`
	Duration         string   `json:"duration"`
}

// Marker is what an import leaves behind.
type Marker struct {
	Version int `json:"version"`
	// Archive identifies the archive, without the presigned query string, which
	// is a credential and differs between two URLs of the same object.
	Archive          string    `json:"archive"`
	ImportedAt       time.Time `json:"importedAt"`
	Restored         int       `json:"restored"`
	Deleted          int       `json:"deleted"`
	Relaunched       []string  `json:"relaunched,omitempty"`
	FailedRelaunches []string  `json:"failedRelaunches,omitempty"`
	// CreatedAt is when the archive was taken.
	CreatedAt time.Time `json:"createdAt"`
	// Partial records an import that failed after it had written to the
	// filesystem. The marker is written for those too, and it is what keeps the
	// archive from being applied a second time over the mix of the image's files
	// and the archive's that the failure left: the freeze protecting that
	// filesystem does not survive sandbox-api restarting, and a re-import would
	// restore over files a first import had already changed.
	Partial bool `json:"partial,omitempty"`
	// Error is why a partial import stopped, for whoever comes to look.
	Error string `json:"error,omitempty"`
	// Started records an import that began and has not reported an outcome. It
	// is written before the first member is restored and replaced by the
	// import's own record, so a start that finds it knows the filesystem may be
	// a mix of the image's files and the archive's: the process was killed - an
	// OOM, a crash - while it was writing, which leaves nothing else behind
	// since the freeze lives in memory and the quarantine never ran.
	Started bool `json:"started,omitempty"`
	// Wrote says that import had begun writing when it stopped. One killed
	// before that - during its download, which is where a large restore spends
	// most of its time - left the image's files untouched, and the start that
	// follows it is an ordinary first import: failing it is not a partial
	// import, and must not quarantine a filesystem nothing ever changed.
	Wrote bool `json:"wrote,omitempty"`
	// PendingProcesses is the archived process list, carried by the marker for as
	// long as those processes have not been started. The record has to be written
	// before anything is relaunched - a crash after the relaunch and before the
	// record would import the archive again over a filesystem already running it -
	// so without this the same crash the other way around leaves a filesystem that
	// looks fully imported and a workload that was never started. Carrying the
	// list lets the next start finish the relaunch instead.
	PendingProcesses json.RawMessage `json:"pendingProcesses,omitempty"`
}

func (o ImportOptions) relaunchProcesses() bool {
	return o.RelaunchProcesses == nil || *o.RelaunchProcesses
}

func (o ImportOptions) rootDir() string {
	if o.root != "" {
		return o.root
	}
	return DefaultRoot
}

func (o ImportOptions) markerPath() string {
	if o.MarkerPath != "" {
		return o.MarkerPath
	}
	if o.root != "" {
		return filepath.Join(o.root, strings.TrimPrefix(DefaultImportMarker, "/"))
	}
	return DefaultImportMarker
}

// ImportOnBoot restores the archive named by EnvImportURL, once.
//
// It returns ErrNoImport when there is nothing to do, which is the normal case:
// no URL, or a marker saying this filesystem already carries the archive.
func ImportOnBoot(ctx context.Context) (*ImportResult, error) {
	return importOnBoot(ctx, ImportOptions{URL: os.Getenv(EnvImportURL)})
}

func importOnBoot(ctx context.Context, options ImportOptions) (_ *ImportResult, err error) {
	if options.URL == "" {
		return nil, ErrNoImport
	}

	// The boot froze the sandbox before it started serving, since an import
	// running behind the API must not have its freeze arrive after the first
	// request. Everything below that ends without importing has to give the
	// sandbox back, or it would answer as frozen forever. Import ends its own
	// freeze, and a partial import keeps it on purpose.
	imported := false
	defer func() {
		if !imported && !errors.Is(err, ErrPartialImport) {
			cancelRestore()
		}
	}()

	identity := archiveIdentity(options.URL)
	markerPath := options.markerPath()
	marker, err := readMarker(markerPath)
	if err != nil {
		return nil, err
	}
	// retrying is an import that follows one killed while it was writing. The
	// archive is restored again, over whatever that attempt left, and this time
	// any failure is a partial import: the filesystem is only the image's again
	// if this import finishes.
	retrying := false
	if marker != nil {
		// Any marker stops the import, not only one for this archive: the
		// filesystem has already been restored once, and applying another
		// archive over it would mix two sandboxes' state.
		if marker.Partial {
			// The filesystem is the mix a failed import left, and it stays that
			// way: the workload must not start on it, which is what reporting a
			// partial import to the caller does.
			return nil, fmt.Errorf("%w: an earlier import of %s stopped after writing to the filesystem (%s)",
				ErrPartialImport, marker.Archive, marker.Error)
		}
		if len(marker.PendingProcesses) > 0 {
			// The filesystem carries the archive and its workload was never
			// started: the import was recorded and this API stopped before it
			// relaunched anything. Restoring again is not what is missing, the
			// processes are.
			imported = true
			enterRestoreState(RestoreRelaunching)
			result, err := resumeRelaunch(options, *marker)
			endRestore(result, err)
			return result, err
		}
		if !marker.Started {
			logrus.WithFields(logrus.Fields{
				"archive":    marker.Archive,
				"importedAt": marker.ImportedAt,
				"requested":  identity,
			}).Info("[Archive] Filesystem already restored, skipping the import")
			return nil, ErrNoImport
		}
		// Only an attempt that had begun writing leaves a filesystem this one
		// has to finish restoring. One killed during its download changed
		// nothing, and treating it as a retry would turn any later failure -
		// an expired URL, a storage hiccup - into a quarantine of an untouched
		// sandbox.
		retrying = marker.Wrote
		logrus.WithFields(logrus.Fields{
			"archive":   marker.Archive,
			"startedAt": marker.ImportedAt,
			"wrote":     marker.Wrote,
		}).Warn("[Archive] An earlier import was killed before it reported an outcome, restoring the archive again")
	}

	if status := Status(); status.ReadOnlyRoot {
		// The root was found read-only at startup and no marker says why. What
		// leaves exactly that is an import that failed after writing and could
		// not record it - the quarantine remounts the root, and the marker write
		// is what fails on a filesystem with nowhere left to write. Importing
		// again would restore over the files that first attempt already changed,
		// and reporting anything softer would start the workload on the mix.
		return nil, fmt.Errorf("%w: the root filesystem was already read-only before this import, an earlier one stopped without recording itself", ErrPartialImport)
	}

	logrus.WithField("archive", identity).Info("[Archive] Restoring the filesystem from the archive before the workload starts")
	// The attempt is recorded before it writes anything. Killed in the middle -
	// an OOM while a large archive is being extracted - it has no chance to
	// record anything itself, and without this the half restored filesystem it
	// leaves is indistinguishable from the image's own.
	if err := writeMarker(markerPath, startedMarker(identity)); err != nil {
		return nil, fmt.Errorf("failed to record the start of the import: %w", err)
	}
	// The record says the import is writing only once it is about to, so a kill
	// during the download - where a large restore spends most of its time - is
	// told from one that left a mix behind. An attempt already known to have
	// written keeps its record, since rewriting it would only lose when it
	// began.
	if !retrying {
		options.onWriting = func() {
			writing := startedMarker(identity)
			writing.Wrote = true
			if err := writeMarker(markerPath, writing); err != nil {
				logrus.WithError(err).Error("[Archive] Failed to record that the import started writing, a kill from here on would read as an attempt that wrote nothing")
			}
		}
	}
	options.onRestored = func(result *ImportResult, pendingProcesses []byte) error {
		marker := markerFor(identity, result)
		marker.PendingProcesses = pendingProcesses
		return writeMarker(markerPath, marker)
	}

	imported = true
	result, err := Import(ctx, options)
	if err != nil {
		if retrying && !errors.Is(err, ErrPartialImport) {
			// Nothing was written this time, but the attempt this one follows
			// was writing when it died: the filesystem stays the mix it left,
			// and the workload must not run on it.
			err = fmt.Errorf("%w: an earlier import was killed while it was writing and this one could not restore the archive again: %w", ErrPartialImport, err)
		}
		if errors.Is(err, ErrPartialImport) {
			// The filesystem is a mix of the image and the archive, and it is
			// quarantined here rather than by the caller: until the root is
			// remounted read-only, a terminal - a route the restore's freeze
			// still serves - writes into that mix.
			//
			// The record goes in while the root is still writable, since the
			// next start of sandbox-api would otherwise restore the archive
			// again over what this one already wrote. Nothing else can hold
			// that back: the freeze lives in memory, and the read-only remount
			// may not have taken.
			if quarantineErr := quarantineWhile(options.rootDir(), "failed archive import", func() {
				if markerErr := writeMarker(markerPath, partialMarker(identity, err)); markerErr != nil {
					logrus.WithError(markerErr).Error("[Archive] Failed to record a partially restored filesystem, another start would restore the archive over it again")
				}
			}); quarantineErr != nil {
				logrus.WithError(quarantineErr).Error("[Archive] Failed to freeze the sandbox after a partial import")
			}
			return nil, err
		}
		// The import failed before writing anything - an archive that could not
		// be downloaded - so the filesystem is the image's and the sandbox boots
		// on it. The attempt is forgotten, since a later start reading it would
		// refuse to run a workload on a filesystem nothing ever touched.
		if removeErr := os.Remove(markerPath); removeErr != nil && !os.IsNotExist(removeErr) {
			logrus.WithError(removeErr).Warn("[Archive] Failed to forget an import that wrote nothing")
		}
		return nil, err
	}

	// The processes exist now, so the record can name them - and name the ones
	// that could not be started, which is the only lasting trace that part of the
	// workload did not come back. It also drops the list of processes still to
	// start, which is what keeps another start from relaunching them. Failing
	// leaves that list behind, and a relaunch already done is skipped by name, so
	// the worst it costs is the attempt.
	if err := writeMarker(markerPath, markerFor(identity, result)); err != nil {
		logrus.WithError(err).Warn("[Archive] Failed to record the relaunched processes of the import")
	}
	return result, nil
}

// resumeRelaunch starts the processes an import restored the filesystem for but
// never got to launch. It reports the import as the one that happened, since
// that is what the sandbox now carries.
func resumeRelaunch(options ImportOptions, marker Marker) (*ImportResult, error) {
	logrus.WithFields(logrus.Fields{
		"archive":    marker.Archive,
		"importedAt": marker.ImportedAt,
	}).Info("[Archive] Filesystem already restored, relaunching the workload the earlier import did not start")

	result := &ImportResult{
		Restored: marker.Restored,
		Deleted:  marker.Deleted,
	}
	if options.relaunchProcesses() {
		result.Relaunched, result.FailedRelaunches = relaunch(options.rootDir(), marker.PendingProcesses)
	}

	marker.PendingProcesses = nil
	marker.Relaunched = append(marker.Relaunched, result.Relaunched...)
	marker.FailedRelaunches = append(marker.FailedRelaunches, result.FailedRelaunches...)
	if err := writeMarker(options.markerPath(), marker); err != nil {
		logrus.WithError(err).Warn("[Archive] Failed to record the relaunched processes of the import")
	}
	return result, nil
}

func markerFor(identity string, result *ImportResult) Marker {
	return Marker{
		Version:          ManifestVersion,
		Archive:          identity,
		ImportedAt:       time.Now(),
		Restored:         result.Restored,
		Deleted:          result.Deleted,
		Relaunched:       result.Relaunched,
		FailedRelaunches: result.FailedRelaunches,
		CreatedAt:        result.Manifest.CreatedAt,
	}
}

// startedMarker is what an import writes before it restores anything, so a
// filesystem it was killed halfway through is never read as the image's own.
func startedMarker(identity string) Marker {
	return Marker{
		Version:    ManifestVersion,
		Archive:    identity,
		ImportedAt: time.Now(),
		Started:    true,
	}
}

// partialMarker is what a failed import leaves behind, so the filesystem it
// half restored is never restored over again.
func partialMarker(identity string, cause error) Marker {
	return Marker{
		Version:    ManifestVersion,
		Archive:    identity,
		ImportedAt: time.Now(),
		Partial:    true,
		Error:      cause.Error(),
	}
}

// Import downloads the archive and applies it to the filesystem: the members are
// written, the manifest's deletions are applied, and the processes the archive
// recorded are relaunched from their command line.
//
// It is meant to run before the workload starts. Nothing here freezes the
// sandbox: an import that races the workload is a caller's mistake, not
// something this can detect.
func Import(ctx context.Context, options ImportOptions) (_ *ImportResult, err error) {
	if options.URL == "" {
		return nil, ErrNoImport
	}

	// A sandbox restoring a large archive has no workload running yet and no
	// connection to answer, so it looks idle for as long as the download takes -
	// long enough for the infrastructure to hibernate it in the middle of the
	// restore.
	defer blaxel.HoldAwake("the archive import")()

	// The download runs before anything is served, so a storage stall would hold
	// up the boot: it covers reading the body too, which is where a stalled
	// transfer actually hangs.
	ctx, cancel := context.WithTimeout(ctx, transferTimeout)
	defer cancel()

	// The sandbox exists and answers while this runs, so how far the restore
	// has got is reported all along rather than only at the end - and the
	// mutating routes are refused until it is done, since the filesystem is
	// neither the image's nor the archive's until then.
	beginRestore("restoring the archived filesystem")
	var result *ImportResult
	defer func() { endRestore(result, err) }()

	started := time.Now()
	body, size, err := download(ctx, options.URL)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	recordArchiveSize(size)
	enterRestoreState(RestoreExtracting)

	var processes []byte
	result, processes, err = extract(&countingReader{reader: body}, options)
	if err != nil {
		return nil, err
	}

	// The filesystem now carries the archive, so the import is recorded before
	// anything runs on top of it. A failure here is fatal on purpose: the
	// sandbox cannot promise to import the archive only once, and importing it
	// twice would undo whatever ran in between.
	if options.onRestored != nil {
		var pending []byte
		if options.relaunchProcesses() {
			pending = processes
		}
		if err := options.onRestored(result, pending); err != nil {
			return nil, fmt.Errorf("%w: failed to record the import: %w", ErrPartialImport, err)
		}
	}

	if options.relaunchProcesses() && len(processes) > 0 {
		enterRestoreState(RestoreRelaunching)
		result.Relaunched, result.FailedRelaunches = relaunch(options.rootDir(), processes)
	}

	result.Duration = time.Since(started).String()
	logrus.WithFields(logrus.Fields{
		"restored":   result.Restored,
		"deleted":    result.Deleted,
		"relaunched": len(result.Relaunched),
		"bytes":      result.Bytes,
		"duration":   result.Duration,
	}).Info("[Archive] Import complete")
	return result, nil
}

// download fetches the archive. The URL is presigned, so it is a credential:
// it never reaches an error message or a log line.
// The size it returns is what the storage announced, and zero when it announced
// nothing: it is only used to say how far the restore has got.
func download(ctx context.Context, url string) (io.ReadCloser, int64, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to build the download request: %w", err)
	}

	response, err := transferClient.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to download the archive: %w", redactURL(err))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		message, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return nil, 0, fmt.Errorf("archive download rejected with status %d: %s", response.StatusCode, redactAnswer(message))
	}
	return response.Body, response.ContentLength, nil
}

// extract applies the archive to the filesystem and returns the process state it
// carried, if any. Both compressed and uncompressed archives are accepted: the
// export writes plain tar, but the manual procedure this replaces produced
// tar.gz and those archives stay readable.
func extract(body io.Reader, options ImportOptions) (_ *ImportResult, _ []byte, err error) {
	// The archive is applied to the live filesystem, so a failure halfway through
	// leaves a filesystem that is neither the image's nor the archive's. Nothing
	// can be rolled back - the point of the import is that the workload starts on
	// top of these files - so the one thing that must not happen is starting the
	// workload on top of half of them without anyone knowing.
	written := 0
	// announceWriting tells the caller, once, that the filesystem is about to
	// stop being the image's. It runs before the write rather than after, since
	// the kill this guards against can land in the middle of one.
	announced := false
	announceWriting := func() {
		if announced || options.onWriting == nil {
			return
		}
		announced = true
		options.onWriting()
	}
	defer func() {
		if err != nil && written > 0 {
			err = fmt.Errorf("%w: %w", ErrPartialImport, err)
		}
	}()

	buffered := bufio.NewReader(body)
	var reader io.Reader = buffered
	if magic, err := buffered.Peek(2); err == nil && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(buffered)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read the compressed archive: %w", err)
		}
		defer gz.Close()
		reader = gz
	}

	root := options.rootDir()
	excludes := append(append([]string(nil), DefaultExcludes...), options.Excludes...)
	excludes = append(excludes, executableExcludes()...)
	result := &ImportResult{}
	var processes []byte

	tr := tar.NewReader(reader)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read the archive: %w", err)
		}

		name, err := memberName(header.Name)
		if err != nil {
			return nil, nil, err
		}
		switch name {
		case ManifestName:
			if err := json.NewDecoder(io.LimitReader(tr, maxMetadataBytes)).Decode(&result.Manifest); err != nil {
				return nil, nil, fmt.Errorf("failed to read the archive manifest: %w", err)
			}
			if result.Manifest.Version > ManifestVersion {
				return nil, nil, fmt.Errorf("archive format version %d is newer than this sandbox understands (%d)", result.Manifest.Version, ManifestVersion)
			}
			continue
		case ProcessesName:
			if processes, err = readMetadata(tr, ProcessesName); err != nil {
				return nil, nil, err
			}
			continue
		}
		if name == MetadataDir || strings.HasPrefix(name, MetadataDir+"/") {
			continue
		}

		// The excludes are applied again here, not only on export: an archive is
		// a file the sandbox is handed, and one carrying etc/resolv.conf or a
		// path climbing out of the root must not be able to use it.
		if excludedPath(name, excludes) {
			result.Skipped = append(result.Skipped, name)
			continue
		}
		announceWriting()
		wrote, err := restore(root, name, excludes, header, tr)
		if wrote {
			// Counted before the error is looked at: a member that failed
			// halfway still changed the filesystem, and that is what decides
			// whether the workload may start on it.
			written++
		}
		if err != nil {
			return nil, nil, err
		}
		// A member of a type the sandbox does not restore changed nothing, so it
		// counts as skipped: were it counted as written, a later failure would be
		// reported as a partial import - and a partial import keeps the workload
		// from starting - over a filesystem nothing had touched yet.
		if !wrote {
			result.Skipped = append(result.Skipped, name)
			continue
		}
		result.Restored++
		result.Bytes += header.Size
	}

	if len(result.Manifest.Deleted) > 0 {
		announceWriting()
	}
	deleted, err := applyDeletions(root, result.Manifest.Deleted, excludes)
	written += deleted
	if err != nil {
		return nil, nil, err
	}
	result.Deleted = deleted
	return result, processes, nil
}

// memberName is the name an archive member is applied under: relative to the
// root, without a traversal component. A name is cleaned before it is compared
// to the excludes, so "etc/../etc/resolv.conf" cannot smuggle a path past them.
func memberName(name string) (string, error) {
	slash := filepath.ToSlash(name)
	for _, part := range strings.Split(slash, "/") {
		if part == ".." {
			return "", fmt.Errorf("archive member %q escapes the root", name)
		}
	}
	clean := strings.Trim(path.Clean("/"+slash), "/")
	if clean == "" || clean == "." {
		return "", fmt.Errorf("archive member %q has no name", name)
	}
	return clean, nil
}

// resolve turns an archive member name into a path inside the root, refusing the
// ones that would leave it — either by climbing out, or by being written through
// a symlink that the image, or an earlier member, put on the way. Without the
// second check a member named "link/resolv.conf", under a "link -> /etc" member,
// writes outside everything the excludes protect.
func resolve(root, name string) (string, error) {
	clean, err := memberName(name)
	if err != nil {
		return "", err
	}

	parts := strings.Split(clean, "/")
	target := root
	for _, part := range parts[:len(parts)-1] {
		target = filepath.Join(target, part)
		info, err := os.Lstat(target)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf("failed to check %s: %w", target, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("archive member %q would be restored through the symlink %s", name, target)
		}
	}
	return filepath.Join(target, parts[len(parts)-1]), nil
}

// restore writes one archive member, with the mode, ownership and modification
// time it was archived with. Ownership is best effort: it needs privileges the
// API does not always have, and a restored file the workload can read is better
// than a failed import.
//
// It reports whether the filesystem was touched, which a failure can also do:
// a member removed before it could be rewritten leaves a filesystem that is
// neither the image's nor the archive's, and the caller decides what to make of
// that. It is false for a member of a type this does not restore, and for one
// that failed before it changed anything.
func restore(root, name string, excludes []string, header *tar.Header, content io.Reader) (bool, error) {
	target := filepath.Join(root, filepath.FromSlash(name))

	// Excluding a path only protects that path; the directory holding it is not
	// excluded, since the archive has to be able to restore what else lives in
	// it. Replacing such a directory with something that is not one, though,
	// takes everything below it away - a symlink named "etc" would carry off the
	// resolver configuration and the hostname the platform injected, none of
	// which appear as members of their own. An archive is data the sandbox is
	// handed, so it does not get to do that.
	if header.Typeflag != tar.TypeDir && excludesUnder(name, excludes) {
		return false, fmt.Errorf("archive member %q would replace a directory holding paths that are never restored", name)
	}

	switch header.Typeflag {
	case tar.TypeDir, tar.TypeSymlink, tar.TypeLink, tar.TypeReg:
	default:
		// Devices, fifos and sockets: the export never produces them, since the
		// devices live on a tmpfs that is not archived. Nothing is written for
		// them, not even the directory holding them.
		logrus.WithFields(logrus.Fields{"path": target, "type": header.Typeflag}).Warn("[Archive] Skipping an archive member of an unsupported type")
		return false, nil
	}

	// Creating the parents is a write of its own, and one a failure can leave
	// behind: the components made before it stops exist afterwards, and the
	// filesystem then holds directories the image did not have. Every failure
	// below carries that state on, and adds its own only when it did remove or
	// write something - a failure that changed nothing must not be reported as
	// partial, or the sandbox is quarantined over a filesystem that is still
	// exactly the image's.
	parent, touched, err := openParent(root, name, true)
	if err != nil {
		return touched, err
	}
	defer parent.Close()
	// Everything below writes through the directory held open rather than
	// through the path, which the workload's user may still be changing.
	writable := parent.path()

	switch header.Typeflag {
	case tar.TypeDir:
		// The path may hold the image's file, or symlink, where the archived
		// sandbox has a directory: mkdir over it fails with ENOTDIR, and the
		// type change is exactly what the archive is there to reproduce.
		if info, err := os.Lstat(writable); err == nil && !info.IsDir() {
			if excludesUnder(name, excludes) {
				return touched, fmt.Errorf("archive member %q would replace %s, which holds paths that are never restored", name, target)
			}
			if err := os.Remove(writable); err != nil {
				return touched, fmt.Errorf("failed to replace %s: %w", target, err)
			}
			touched = true
		}
		// A directory holding paths that are never restored - etc, var/lib/blaxel -
		// keeps the mode and ownership the image gave it. Taking the archive's
		// instead would let it hand the resolver configuration, the hostname or this
		// API's own state to the workload without ever naming those files as
		// members, which is exactly what excluding them is for.
		if excludesUnder(name, excludes) {
			// The image already holds these directories, so restoring one usually
			// changes nothing at all - and a change that did not happen must not
			// be reported, or a later failure would quarantine a sandbox whose
			// filesystem is still the image's.
			created := !exists(writable)
			if err := os.Mkdir(writable, 0o755); err != nil && !os.IsExist(err) {
				return touched, fmt.Errorf("failed to create %s: %w", target, err)
			}
			return touched || created, nil
		}
		if !exists(writable) {
			touched = true
		}
		if err := os.Mkdir(writable, header.FileInfo().Mode().Perm()); err != nil && !os.IsExist(err) {
			return touched, fmt.Errorf("failed to create %s: %w", target, err)
		}
	case tar.TypeSymlink:
		// A symlink cannot be reopened to be rewritten, and the path may hold
		// the image's version of it. Removing nothing is not a change: the link
		// failing afterwards then leaves the filesystem as the image had it.
		removed := exists(writable)
		if err := os.RemoveAll(writable); err != nil {
			return touched, fmt.Errorf("failed to replace %s: %w", target, err)
		}
		if err := os.Symlink(header.Linkname, writable); err != nil {
			return touched || removed, fmt.Errorf("failed to link %s: %w", target, err)
		}
		// A symlink's own mode and times are not the target's, and lchown is the
		// only one of the three that means anything here.
		if err := os.Lchown(writable, header.Uid, header.Gid); err != nil {
			logrus.WithError(err).WithField("path", target).Debug("[Archive] Failed to restore the symlink ownership")
		}
		return true, nil
	case tar.TypeLink:
		// A hardlink's target is a member name, relative to the archive root,
		// not to the member's own directory.
		linkname, err := memberName(header.Linkname)
		if err != nil {
			return touched, err
		}
		// Hardlinking an excluded path into the restored tree would hand the
		// archive's owner the live file - a platform credential under bl/, the
		// resolver configuration - under a name they choose, which is what the
		// excludes exist to prevent.
		if excludedPath(linkname, excludes) {
			return touched, fmt.Errorf("archive member %q would hardlink %q, which is never restored", name, linkname)
		}
		// The source is reached through its own directory held open, for the
		// same reason the member is: a component of it turning into a symlink
		// between the check and the link would hand the archive's owner a file
		// they never named.
		sourceDir, _, err := openParent(root, linkname, false)
		if err != nil {
			return touched, err
		}
		defer sourceDir.Close()
		removed := exists(writable)
		if err := os.RemoveAll(writable); err != nil {
			return touched, fmt.Errorf("failed to replace %s: %w", target, err)
		}
		if err := os.Link(sourceDir.path(), writable); err != nil {
			return touched || removed, fmt.Errorf("failed to hardlink %s: %w", target, err)
		}
		return true, nil
	case tar.TypeReg:
		changed, err := writeFile(writable, target, header.FileInfo().Mode().Perm(), content)
		if changed {
			// Carried past the error too: the mode and the times are set below,
			// and a failure there comes after the content was already replaced.
			touched = true
		}
		if err != nil {
			return touched, err
		}
	}

	if err := restoreMetadata(parent, header, target); err != nil {
		return touched, err
	}
	return true, nil
}

// restoreMetadata sets the ownership, the mode and the times of the member that
// was just written. They are set on the descriptor the member is opened as, and
// the member is opened without following it: chown, chmod and chtimes all
// follow the last component of a path, so a name swapped for a symlink between
// the write and these calls would otherwise carry a root chmod - a setuid bit,
// a world-writable mode - onto a file the archive never named.
func restoreMetadata(parent *parentDir, header *tar.Header, target string) error {
	member, err := parent.open(header.Typeflag == tar.TypeDir)
	if err != nil {
		return fmt.Errorf("failed to reopen %s: %w", target, err)
	}
	defer member.Close()

	if err := member.Chown(header.Uid, header.Gid); err != nil {
		logrus.WithError(err).WithField("path", target).Debug("[Archive] Failed to restore the ownership")
	}
	if err := member.Chmod(header.FileInfo().Mode().Perm()); err != nil {
		return fmt.Errorf("failed to set the mode of %s: %w", target, err)
	}
	if !header.ModTime.IsZero() {
		if err := setTimes(member, header.ModTime); err != nil {
			logrus.WithError(err).WithField("path", target).Debug("[Archive] Failed to restore the modification time")
		}
	}
	return nil
}

// writeFile replaces target's content. The file is written and renamed, so a
// binary the sandbox is running is replaced rather than overwritten in place,
// which would fail with ETXTBSY.
//
// It reports whether the filesystem was changed, which a failure does not do on
// its own: the content goes to a temporary file that is removed again, so a
// download cut short leaves the image's file as it was.
// The path written to is the one reached through the directory held open, and
// name is the same path as it reads, which is what the errors carry.
func writeFile(target, name string, mode os.FileMode, content io.Reader) (bool, error) {
	changed := false
	if info, err := os.Lstat(target); err == nil && info.IsDir() {
		if err := os.RemoveAll(target); err != nil {
			return true, fmt.Errorf("failed to replace the directory %s: %w", name, err)
		}
		changed = true
	}

	// A name of its own, in the target's directory, rather than one derived
	// from the target: an archive carrying both "app" and "app.blaxel-import"
	// would otherwise have the second member removed to stage the first, which
	// is a member of the archive deleted by the import that was restoring it.
	// CreateTemp opens with O_CREATE|O_EXCL, so it neither follows a symlink
	// standing under the name it picked nor replaces anything.
	file, err := os.CreateTemp(filepath.Dir(target), ".blaxel-import-*")
	if err != nil {
		return changed, fmt.Errorf("failed to create %s: %w", name, err)
	}
	temporary := file.Name()
	// CreateTemp opens at 0600, and the file is renamed onto the target, so the
	// archived mode has to be set on it before it is installed.
	if err := file.Chmod(mode); err != nil {
		file.Close()
		os.Remove(temporary)
		return changed, fmt.Errorf("failed to set the mode of %s: %w", name, err)
	}
	if _, err := io.Copy(file, content); err != nil {
		file.Close()
		os.Remove(temporary)
		return changed, fmt.Errorf("failed to write %s: %w", name, err)
	}
	if err := file.Close(); err != nil {
		os.Remove(temporary)
		return changed, fmt.Errorf("failed to close %s: %w", name, err)
	}
	if err := os.Rename(temporary, target); err != nil {
		os.Remove(temporary)
		return changed, fmt.Errorf("failed to install %s: %w", name, err)
	}
	return true, nil
}

// applyDeletions removes the paths the archived sandbox had deleted from the
// image. Tar cannot carry a deletion, so they travel in the manifest.
func applyDeletions(root string, deleted, excludes []string) (int, error) {
	// Deepest first, so a directory is removed after what the manifest lists
	// inside it rather than taking it along.
	paths := append([]string(nil), deleted...)
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))

	count := 0
	for _, deletion := range paths {
		name, err := memberName(deletion)
		if err != nil {
			// The count is returned along with the error: what was already
			// removed stays removed, and the caller has to know the filesystem
			// was touched.
			return count, err
		}
		if excludedPath(name, excludes) {
			continue
		}
		// Deleting a directory takes what is inside it along, including the paths
		// that are never restored and that the platform injected into this VM.
		if excludesUnder(name, excludes) {
			logrus.WithField("path", name).Warn("[Archive] Refusing a deletion that would remove paths that are never restored")
			continue
		}
		parent, _, err := openParent(root, name, false)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// The image never had the directory holding it, so there is
				// nothing to delete.
				continue
			}
			return count, err
		}
		// RemoveAll, not Remove: a directory the archived sandbox deleted is
		// recorded as the one path it deleted, and the image it is being
		// restored over still fills that directory with the entries the
		// deletion was meant to take along.
		// A deletion that cannot be applied is fatal, like a member that cannot
		// be written: the filesystem would carry a path the archived sandbox had
		// removed, so it is not the filesystem the archive describes, and the
		// workload would start on a state that never existed.
		// A path the image never had is nothing to remove, and counting it would
		// report a filesystem the import has not touched as changed.
		removable := parent.path()
		if !exists(removable) {
			parent.Close()
			continue
		}
		err = os.RemoveAll(removable)
		parent.Close()
		if err != nil {
			return count, fmt.Errorf("failed to apply the deletion of %s: %w", name, err)
		}
		count++
	}
	return count, nil
}

// platformPaths are the trees that belong to the platform rather than to the
// workload: the credentials and metadata injected into this VM, and this API's
// own state. They are excluded from an archive, and unlike the other excluded
// paths - tmp, run - nothing may be created inside them on the archive's word.
var platformPaths = []string{
	"bl",
	"run/secrets",
	"var/run/secrets",
	"var/lib/blaxel",
	"var/log/sandbox-api",
	"mnt/blaxel-archive-image",
}

// restorableWorkingDir reports whether a working directory an archive recorded
// may be created here. The process list is part of the archive, so a crafted one
// names any directory it likes, and creating it would put directories of the
// archive's choosing inside the platform's own trees, or through a symlink a
// restored member left on the way there.
func restorableWorkingDir(root, dir string) error {
	if !filepath.IsAbs(dir) {
		// The archive records the absolute path the process ran in. Anything
		// else would be created relative to wherever this API happens to run.
		return fmt.Errorf("working directory %q is not absolute", dir)
	}
	name, err := memberName(dir)
	if err != nil {
		return err
	}
	if excludedPath(name, platformPaths) {
		return fmt.Errorf("working directory %q belongs to the platform, not to the workload", dir)
	}
	_, err = resolve(root, name)
	return err
}

// restorableProcessName reports whether a name an archive recorded may be given
// to a relaunched process. The name is not just a label: the process manager
// builds this process's log files out of it, and opens them as root. A name
// carrying a path of its own would therefore have a crafted process list decide
// what those root-privileged writes truncate, anywhere on the filesystem.
func restorableProcessName(name string) error {
	if name == "" {
		return errors.New("process name is empty")
	}
	if name != filepath.Base(name) || name == "." || name == ".." {
		return fmt.Errorf("process name %q is a path, not a name", name)
	}
	if strings.ContainsRune(name, os.PathSeparator) || strings.ContainsRune(name, 0) {
		return fmt.Errorf("process name %q contains a path separator", name)
	}
	return nil
}

// runsUnderName reports whether a process of that name is already running. It
// walks the processes rather than asking for the name as an identifier: an
// identifier that reads as a number is a PID there, so a process the workload
// named "42" would never be found by name and a resumed relaunch would start a
// second copy of it.
func runsUnderName(pm *process.ProcessManager, name string) bool {
	for _, live := range pm.ListProcesses() {
		if live.Name == name && live.Status == process.StatusRunning {
			return true
		}
	}
	return false
}

// relaunch starts the processes the archive recorded as running, oldest first,
// and reports the identifiers of the ones that started along with the names of
// the ones that did not.
//
// They are started, not adopted: the archive carries no memory, so the PIDs it
// records belong to a VM that no longer exists. Each process keeps its name, so
// a caller that knew it by name still finds it, but its identifier is new.
func relaunch(root string, state []byte) (relaunched, failed []string) {
	var saved process.ManagerState
	if err := json.Unmarshal(state, &saved); err != nil {
		logrus.WithError(err).Error("[Archive] Failed to read the archived process list, the workload is not relaunched")
		return nil, nil
	}

	candidates := make([]process.ProcessState, 0, len(saved.Processes))
	for _, archived := range saved.Processes {
		if archived.Status != process.StatusRunning {
			continue
		}
		candidates = append(candidates, archived)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].StartedAt.Before(candidates[j].StartedAt) })

	pm := process.GetProcessManager()
	for _, archived := range candidates {
		if err := restorableProcessName(archived.Name); err != nil {
			logrus.WithError(err).WithField("command", archived.Command).
				Error("[Archive] Refusing to relaunch an archived process, its name is not one")
			failed = append(failed, archived.Name)
			continue
		}
		// The process manager refuses to start a process whose working directory
		// is missing, and a directory the workload made under a path that is
		// never archived - tmp, run - is missing on a restored sandbox. The
		// directory is recreated rather than the process dropped: what the
		// archive promises is the workload running again, and an empty working
		// directory is what the archive says that path holds.
		if archived.WorkingDir != "" && !exists(archived.WorkingDir) {
			if err := restorableWorkingDir(root, archived.WorkingDir); err != nil {
				logrus.WithError(err).WithFields(logrus.Fields{
					"name":       archived.Name,
					"workingDir": archived.WorkingDir,
				}).Error("[Archive] Refusing to recreate the working directory of an archived process")
				failed = append(failed, archived.Name)
				continue
			}
			if err := os.MkdirAll(archived.WorkingDir, 0o755); err != nil {
				logrus.WithError(err).WithFields(logrus.Fields{
					"name":       archived.Name,
					"workingDir": archived.WorkingDir,
				}).Error("[Archive] Failed to recreate the working directory of an archived process")
				failed = append(failed, archived.Name)
				continue
			}
			logrus.WithFields(logrus.Fields{
				"name":       archived.Name,
				"workingDir": archived.WorkingDir,
			}).Info("[Archive] Recreated the working directory of an archived process, which the archive does not carry")
		}

		if runsUnderName(pm, archived.Name) {
			// An earlier relaunch already started it: the record naming it as still
			// to be started could not be updated, and a second copy of the workload
			// is worse than a record that lags.
			logrus.WithField("name", archived.Name).Info("[Archive] The archived process already runs, not relaunching it")
			continue
		}

		identifier, err := pm.StartProcessWithName(
			archived.Command,
			archived.WorkingDir,
			archived.Name,
			archived.Env,
			archived.RestartOnFailure,
			archived.MaxRestarts,
			archived.KeepAlive,
			archived.Timeout,
			archived.Stdin,
			// The process manager calls the completion callback unconditionally,
			// so a relaunched process exiting must find something to call.
			func(finished *process.ProcessInfo) {
				logrus.WithFields(logrus.Fields{
					"name":      finished.Name,
					"pid":       finished.PID,
					"status":    finished.Status,
					"exit_code": finished.ExitCode,
				}).Info("[Archive] A relaunched process exited")
			},
		)
		if err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"name":    archived.Name,
				"command": archived.Command,
			}).Error("[Archive] Failed to relaunch an archived process")
			failed = append(failed, archived.Name)
			continue
		}
		relaunched = append(relaunched, identifier)
		logrus.WithFields(logrus.Fields{
			"name":       archived.Name,
			"command":    archived.Command,
			"identifier": identifier,
		}).Info("[Archive] Relaunched an archived process")
	}
	// The relaunched processes exist only in memory until the state is written:
	// this runs on boot, before anything else would save it, so a sandbox-api
	// restarting right after the import would leave them running under PIDs it
	// no longer knows.
	if len(relaunched) > 0 {
		if err := pm.SaveState(); err != nil {
			logrus.WithError(err).Error("[Archive] Failed to persist the relaunched processes")
		}
	}
	return relaunched, failed
}

// redactURL strips the request URL out of a transport error. net/http reports a
// failed request as `Get "<url>": ...`, and the URL is presigned: the reason the
// transfer failed is worth logging, the signature in it is not.
func redactURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("%s %s: %w", urlErr.Op, archiveIdentity(urlErr.URL), urlErr.Err)
	}
	return err
}

// signedMaterial matches the parts of a storage error document that quote the
// request back: an S3 SignatureDoesNotMatch answers with the canonical request
// and the string that was signed, both of which carry the credential and the
// signed query of the presigned URL.
var signedMaterial = func() []*regexp.Regexp {
	names := []string{"StringToSign", "StringToSignBytes", "CanonicalRequest", "CanonicalRequestBytes", "AWSAccessKeyId"}
	patterns := make([]*regexp.Regexp, 0, len(names))
	for _, name := range names {
		patterns = append(patterns, regexp.MustCompile(`(?is)<`+name+`>.*?</`+name+`>`))
	}
	return patterns
}()

// signedParameter matches a signed query parameter wherever it appears in a
// storage answer, since not every store wraps it in an element.
var signedParameter = regexp.MustCompile(`(?i)(x-amz-signature|x-amz-credential|x-amz-security-token|awsaccesskeyid|signature)=[^&\s"'<]*`)

// redactAnswer is a storage answer as it may be reported: what the store said
// went wrong, without the credential it may have quoted back. The answer of a
// failed transfer reaches an error message, a log line and /archive/status,
// and the URL it is about is presigned - a signature copied out of one of
// those is a usable key to the archive until it expires.
func redactAnswer(body []byte) string {
	answer := string(body)
	for _, pattern := range signedMaterial {
		answer = pattern.ReplaceAllString(answer, "[redacted]")
	}
	return signedParameter.ReplaceAllString(answer, "$1=[redacted]")
}

// archiveIdentity is what a presigned URL says about which object it points to:
// its path, without the query string, which carries the signature and the
// expiry and so differs between two URLs of the same archive.
func archiveIdentity(presigned string) string {
	parsed, err := url.Parse(presigned)
	if err != nil {
		return ""
	}
	return parsed.Host + parsed.Path
}

// exists says whether a path is there at all, a broken symlink included: what it
// answers is whether creating it would change the filesystem.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func readMarker(path string) (*Marker, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read the import marker %s: %w", path, err)
	}
	marker := &Marker{}
	if err := json.Unmarshal(data, marker); err != nil {
		// A marker that cannot be read says an import happened without saying
		// how it ended, which is the state a partial one describes: importing
		// again would restore over files the first import wrote, and starting
		// the workload would run it on a filesystem that may be half restored.
		// Neither is safe, so the sandbox is left for someone to look at.
		logrus.WithError(err).WithField("path", path).Error("[Archive] The import marker is unreadable, treating the filesystem as partially restored")
		return &Marker{Partial: true, Error: "the import marker is unreadable"}, nil
	}
	return marker, nil
}

func writeMarker(path string, marker Marker) error {
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize the import marker: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create the import marker directory: %w", err)
	}
	// Written beside the marker and renamed over it: a crash or a full
	// filesystem halfway through a plain write leaves a truncated marker, and a
	// marker that cannot be read is a sandbox nobody can boot any more.
	temporary := path + markerTemporarySuffix
	// O_EXCL and O_NOFOLLOW rather than a plain write: this runs right after an
	// archive was applied to the filesystem, and a write that followed a symlink
	// left under this name would put the marker - as root - wherever the archive
	// pointed. Whatever is there is removed first, since a marker write
	// interrupted earlier leaves the temporary behind.
	if err := removeMarkerTemporary(temporary); err != nil {
		return err
	}
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_WRONLY|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("failed to create the import marker %s: %w", path, err)
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		os.Remove(temporary)
		return fmt.Errorf("failed to write the import marker %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		os.Remove(temporary)
		return fmt.Errorf("failed to write the import marker %s: %w", path, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		os.Remove(temporary)
		return fmt.Errorf("failed to install the import marker %s: %w", path, err)
	}
	return nil
}

// removeMarkerTemporary clears the name the marker is written under, symlink
// included: os.Remove does not follow one, so what it removes is the link and
// never what it points at.
func removeMarkerTemporary(temporary string) error {
	if err := os.RemoveAll(temporary); err != nil {
		return fmt.Errorf("failed to clear the import marker temporary %s: %w", temporary, err)
	}
	return nil
}
