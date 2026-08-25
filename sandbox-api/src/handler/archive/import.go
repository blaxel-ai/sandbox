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
	// over a filesystem already running the first one.
	onRestored func(*ImportResult) error
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
	Bytes      int64    `json:"bytes"`
	Duration   string   `json:"duration"`
}

// Marker is what an import leaves behind.
type Marker struct {
	Version int `json:"version"`
	// Archive identifies the archive, without the presigned query string, which
	// is a credential and differs between two URLs of the same object.
	Archive    string    `json:"archive"`
	ImportedAt time.Time `json:"importedAt"`
	Restored   int       `json:"restored"`
	Deleted    int       `json:"deleted"`
	Relaunched []string  `json:"relaunched,omitempty"`
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

func importOnBoot(ctx context.Context, options ImportOptions) (*ImportResult, error) {
	if options.URL == "" {
		return nil, ErrNoImport
	}

	identity := archiveIdentity(options.URL)
	marker, err := readMarker(options.markerPath())
	if err != nil {
		return nil, err
	}
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
		logrus.WithFields(logrus.Fields{
			"archive":    marker.Archive,
			"importedAt": marker.ImportedAt,
			"requested":  identity,
		}).Info("[Archive] Filesystem already restored, skipping the import")
		return nil, ErrNoImport
	}

	logrus.WithField("archive", identity).Info("[Archive] Restoring the filesystem from the archive before the workload starts")
	markerPath := options.markerPath()
	options.onRestored = func(result *ImportResult) error {
		return writeMarker(markerPath, markerFor(identity, result))
	}

	result, err := Import(ctx, options)
	if err != nil {
		if errors.Is(err, ErrPartialImport) {
			// Recorded so the next start of sandbox-api does not restore the
			// archive again over what this one already wrote. Nothing else can
			// hold that back: the freeze this failure triggers lives in memory,
			// and the read-only root it remounts to may not have taken.
			if markerErr := writeMarker(markerPath, partialMarker(identity, err)); markerErr != nil {
				logrus.WithError(markerErr).Error("[Archive] Failed to record a partially restored filesystem, another start would restore the archive over it again")
			}
		}
		return nil, err
	}

	if len(result.Relaunched) > 0 {
		// The processes exist now, so the record can name them. The import
		// itself is already recorded, which is the part that must not be lost.
		if err := writeMarker(markerPath, markerFor(identity, result)); err != nil {
			logrus.WithError(err).Warn("[Archive] Failed to record the relaunched processes of the import")
		}
	}
	return result, nil
}

func markerFor(identity string, result *ImportResult) Marker {
	return Marker{
		Version:    ManifestVersion,
		Archive:    identity,
		ImportedAt: time.Now(),
		Restored:   result.Restored,
		Deleted:    result.Deleted,
		Relaunched: result.Relaunched,
		CreatedAt:  result.Manifest.CreatedAt,
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
func Import(ctx context.Context, options ImportOptions) (*ImportResult, error) {
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

	started := time.Now()
	body, err := download(ctx, options.URL)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	result, processes, err := extract(body, options)
	if err != nil {
		return nil, err
	}

	// The filesystem now carries the archive, so the import is recorded before
	// anything runs on top of it. A failure here is fatal on purpose: the
	// sandbox cannot promise to import the archive only once, and importing it
	// twice would undo whatever ran in between.
	if options.onRestored != nil {
		if err := options.onRestored(result); err != nil {
			return nil, fmt.Errorf("%w: failed to record the import: %w", ErrPartialImport, err)
		}
	}

	if options.relaunchProcesses() && len(processes) > 0 {
		result.Relaunched = relaunch(processes)
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
func download(ctx context.Context, url string) (io.ReadCloser, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build the download request: %w", err)
	}

	response, err := transferClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("failed to download the archive: %w", redactURL(err))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		message, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return nil, fmt.Errorf("archive download rejected with status %d: %s", response.StatusCode, string(message))
	}
	return response.Body, nil
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
			if err := json.NewDecoder(tr).Decode(&result.Manifest); err != nil {
				return nil, nil, fmt.Errorf("failed to read the archive manifest: %w", err)
			}
			if result.Manifest.Version > ManifestVersion {
				return nil, nil, fmt.Errorf("archive format version %d is newer than this sandbox understands (%d)", result.Manifest.Version, ManifestVersion)
			}
			continue
		case ProcessesName:
			if processes, err = io.ReadAll(tr); err != nil {
				return nil, nil, fmt.Errorf("failed to read the archived process list: %w", err)
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
		target, err := resolve(root, name)
		if err != nil {
			return nil, nil, err
		}

		wrote, err := restore(root, target, name, excludes, header, tr)
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
func restore(root, target, name string, excludes []string, header *tar.Header, content io.Reader) (bool, error) {
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

	// The parents count as a write of their own, whether MkdirAll succeeds or
	// not: it creates the ones it gets through before it stops, and once they
	// exist the filesystem holds directories the image did not have - so every
	// failure below this point is one that already changed the filesystem.
	parent := filepath.Dir(target)
	touched := !exists(parent)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return true, fmt.Errorf("failed to create the parent of %s: %w", target, err)
	}

	switch header.Typeflag {
	case tar.TypeDir:
		// The path may hold the image's file, or symlink, where the archived
		// sandbox has a directory: mkdir over it fails with ENOTDIR, and the
		// type change is exactly what the archive is there to reproduce.
		if info, err := os.Lstat(target); err == nil && !info.IsDir() {
			if excludesUnder(name, excludes) {
				return touched, fmt.Errorf("archive member %q would replace %s, which holds paths that are never restored", name, target)
			}
			if err := os.Remove(target); err != nil {
				return true, fmt.Errorf("failed to replace %s: %w", target, err)
			}
		}
		// A directory holding paths that are never restored - etc, var/lib/blaxel -
		// keeps the mode and ownership the image gave it. Taking the archive's
		// instead would let it hand the resolver configuration, the hostname or this
		// API's own state to the workload without ever naming those files as
		// members, which is exactly what excluding them is for.
		if excludesUnder(name, excludes) {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return true, fmt.Errorf("failed to create %s: %w", target, err)
			}
			return true, nil
		}
		if err := os.MkdirAll(target, header.FileInfo().Mode().Perm()); err != nil {
			return true, fmt.Errorf("failed to create %s: %w", target, err)
		}
	case tar.TypeSymlink:
		// A symlink cannot be reopened to be rewritten, and the path may hold
		// the image's version of it.
		if err := os.RemoveAll(target); err != nil {
			return true, fmt.Errorf("failed to replace %s: %w", target, err)
		}
		if err := os.Symlink(header.Linkname, target); err != nil {
			return true, fmt.Errorf("failed to link %s: %w", target, err)
		}
		// A symlink's own mode and times are not the target's, and lchown is the
		// only one of the three that means anything here.
		if err := os.Lchown(target, header.Uid, header.Gid); err != nil {
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
		source, err := resolve(root, linkname)
		if err != nil {
			return touched, err
		}
		if err := os.RemoveAll(target); err != nil {
			return true, fmt.Errorf("failed to replace %s: %w", target, err)
		}
		if err := os.Link(source, target); err != nil {
			return true, fmt.Errorf("failed to hardlink %s: %w", target, err)
		}
		return true, nil
	case tar.TypeReg:
		if changed, err := writeFile(target, header.FileInfo().Mode().Perm(), content); err != nil {
			return touched || changed, err
		}
	}

	if err := os.Chown(target, header.Uid, header.Gid); err != nil {
		logrus.WithError(err).WithField("path", target).Debug("[Archive] Failed to restore the ownership")
	}
	if err := os.Chmod(target, header.FileInfo().Mode().Perm()); err != nil {
		return true, fmt.Errorf("failed to set the mode of %s: %w", target, err)
	}
	if !header.ModTime.IsZero() {
		if err := os.Chtimes(target, header.ModTime, header.ModTime); err != nil {
			logrus.WithError(err).WithField("path", target).Debug("[Archive] Failed to restore the modification time")
		}
	}
	return true, nil
}

// writeFile replaces target's content. The file is written and renamed, so a
// binary the sandbox is running is replaced rather than overwritten in place,
// which would fail with ETXTBSY.
//
// It reports whether the filesystem was changed, which a failure does not do on
// its own: the content goes to a temporary file that is removed again, so a
// download cut short leaves the image's file as it was.
func writeFile(target string, mode os.FileMode, content io.Reader) (bool, error) {
	changed := false
	if info, err := os.Lstat(target); err == nil && info.IsDir() {
		if err := os.RemoveAll(target); err != nil {
			return true, fmt.Errorf("failed to replace the directory %s: %w", target, err)
		}
		changed = true
	}

	temporary := target + ".blaxel-import"
	// O_EXCL and O_NOFOLLOW rather than O_TRUNC: the name is derived from the
	// archive, so it may already exist as a symlink pointing at a path the
	// import would never write to, and opening it would write through it.
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_WRONLY|os.O_EXCL|syscall.O_NOFOLLOW, mode)
	if os.IsExist(err) {
		if err = os.RemoveAll(temporary); err != nil {
			return changed, fmt.Errorf("failed to replace %s: %w", target, err)
		}
		// Whatever stood under the temporary name is gone, and the archive is
		// what named it: the filesystem is not the image's any more.
		changed = true
		file, err = os.OpenFile(temporary, os.O_CREATE|os.O_WRONLY|os.O_EXCL|syscall.O_NOFOLLOW, mode)
	}
	if err != nil {
		return changed, fmt.Errorf("failed to create %s: %w", target, err)
	}
	if _, err := io.Copy(file, content); err != nil {
		file.Close()
		os.Remove(temporary)
		return changed, fmt.Errorf("failed to write %s: %w", target, err)
	}
	if err := file.Close(); err != nil {
		os.Remove(temporary)
		return changed, fmt.Errorf("failed to close %s: %w", target, err)
	}
	if err := os.Rename(temporary, target); err != nil {
		os.Remove(temporary)
		return changed, fmt.Errorf("failed to install %s: %w", target, err)
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
		target, err := resolve(root, name)
		if err != nil {
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
		if err := os.RemoveAll(target); err != nil {
			return count, fmt.Errorf("failed to apply the deletion of %s: %w", name, err)
		}
		count++
	}
	return count, nil
}

// relaunch starts the processes the archive recorded as running, oldest first.
//
// They are started, not adopted: the archive carries no memory, so the PIDs it
// records belong to a VM that no longer exists. Each process keeps its name, so
// a caller that knew it by name still finds it, but its identifier is new.
func relaunch(state []byte) []string {
	var saved process.ManagerState
	if err := json.Unmarshal(state, &saved); err != nil {
		logrus.WithError(err).Error("[Archive] Failed to read the archived process list, the workload is not relaunched")
		return nil
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
	var relaunched []string
	for _, archived := range candidates {
		identifier, err := pm.StartProcessWithName(
			archived.Command,
			archived.WorkingDir,
			archived.Name,
			archived.Env,
			archived.RestartOnFailure,
			archived.MaxRestarts,
			archived.KeepAlive,
			archived.Timeout,
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
	return relaunched
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
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("failed to write the import marker %s: %w", path, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		os.Remove(temporary)
		return fmt.Errorf("failed to install the import marker %s: %w", path, err)
	}
	return nil
}
