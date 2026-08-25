package archive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/blaxel-ai/sandbox-api/src/handler/process"
	"github.com/blaxel-ai/sandbox-api/src/lib/blaxel"
	"github.com/sirupsen/logrus"
)

const (
	// DefaultMountPoint is where the pristine image is mounted for comparison.
	// It is under /mnt, which the diff never descends into, so the image the
	// export compares against never appears in the archive it produces.
	// /mnt itself is part of the root, not a filesystem of its own: creating
	// this directory fails once the root is read-only, which is why the image is
	// mounted before the root is frozen.
	DefaultMountPoint = "/mnt/blaxel-archive-image"
	// DefaultRoot is the filesystem archived.
	DefaultRoot = "/"
	// DefaultStopTimeout is how long the workload is given to exit after
	// SIGTERM before it is killed.
	DefaultStopTimeout = 30 * time.Second
	// killTimeout bounds the wait for a killed process to be gone.
	killTimeout = 5 * time.Second
	// processPollInterval is how often the stopped processes are looked at while
	// waiting for them to exit.
	processPollInterval = 100 * time.Millisecond
)

// exportMu is held for the whole of an export, dry run included, since they
// share the mountpoint the pristine image is compared from.
var exportMu sync.Mutex

// APIVersion is the sandbox-api build recorded in the manifests it produces.
// Set from the handler, which owns the build information.
var APIVersion = "dev"

// ErrURLRequired is returned for an export with nowhere to upload to, which is
// a bad request rather than a failed export.
var ErrURLRequired error = &InvalidOptionsError{Reason: "url is required unless dryRun is set"}

// InvalidOptionsError is a mistake in the export options rather than a failed
// export, so the request is answered 400 and neither the caller nor a monitor
// reads it as the sandbox failing.
type InvalidOptionsError struct {
	Reason string
}

func (e *InvalidOptionsError) Error() string { return e.Reason }

func invalidOptions(format string, args ...any) error {
	return &InvalidOptionsError{Reason: fmt.Sprintf(format, args...)}
}

// ExportOptions drives an export.
type ExportOptions struct {
	// URL is a presigned S3 PUT URL the archive is streamed to. Empty is only
	// valid with DryRun, or with Multipart.
	URL string `json:"url,omitempty" example:"https://bucket.s3.amazonaws.com/key?..."`
	// Multipart uploads the archive part by part instead, which is how an
	// archive larger than the 5 GB a single PUT accepts is stored. It takes
	// precedence over URL.
	Multipart *MultipartUpload `json:"multipart,omitempty"`
	// Async starts the export and answers immediately, leaving it to run: an
	// archive of a large filesystem takes longer than a request may be held
	// open. Its progress is reported by /archive/status.
	Async bool `json:"async,omitempty" example:"false"`
	// SaveProcesses stores the process list in the archive so restore can
	// relaunch the workload. Defaults to true; set it to false to archive
	// storage only.
	SaveProcesses *bool `json:"saveProcesses,omitempty" example:"true"`
	// DryRun reports what would be archived, and its exact size, without
	// stopping anything and without uploading.
	DryRun bool `json:"dryRun,omitempty" example:"false"`
	// Excludes are added to the paths excluded by default.
	Excludes []string `json:"excludes,omitempty"`
	// ImageDevice is the device holding the pristine image, /dev/vda by default.
	// mk3.0 exposes the same image as a ROM, /dev/ukp_rom0.
	ImageDevice string `json:"imageDevice,omitempty" example:"/dev/vda"`
	// ImageMountPoint is a directory where the pristine image is already mounted.
	// When set the image device is neither mounted nor unmounted.
	ImageMountPoint string `json:"imageMountPoint,omitempty" example:"/mnt/lower"`
	// StopTimeoutSeconds bounds the graceful stop of each process.
	StopTimeoutSeconds int `json:"stopTimeoutSeconds,omitempty" example:"30"`
	// Headers are sent with the upload request as given. A presigned URL only
	// accepts the headers it was signed for, so these have to match what the
	// caller signed: sending one that was not signed, or signing one that is not
	// sent, is rejected as a signature mismatch. Typical use is a storage class,
	// x-amz-storage-class: GLACIER_IR.
	Headers map[string]string `json:"headers,omitempty"`

	// root is only set by tests, which compare two plain directories instead of a
	// live root and a mounted image.
	root string
} // @name ExportOptions

// ExportResult reports an export.
type ExportResult struct {
	Manifest Manifest `json:"manifest" binding:"required"`
	// Size is the exact number of bytes uploaded, known before the upload
	// starts since the archive is not compressed.
	Size int64 `json:"size" example:"3074211"`
	// Uploaded is false for a dry run.
	Uploaded bool `json:"uploaded" example:"true"`
	// StoppedProcesses are the processes stopped to freeze the filesystem.
	StoppedProcesses []string `json:"stoppedProcesses,omitempty"`
	// Changes lists every path in the archive. Only filled for a dry run,
	// where it is the point of the call.
	Changes  []Change `json:"changes,omitempty"`
	Duration string   `json:"duration" example:"4.2s"`
} // @name ExportResult

func (o ExportOptions) saveProcesses() bool {
	return o.SaveProcesses == nil || *o.SaveProcesses
}

func (o ExportOptions) stopTimeout() time.Duration {
	if o.StopTimeoutSeconds > 0 {
		return time.Duration(o.StopTimeoutSeconds) * time.Second
	}
	return DefaultStopTimeout
}

func (o ExportOptions) rootDir() string {
	if o.root != "" {
		return o.root
	}
	return DefaultRoot
}

func (o ExportOptions) imageMountPoint() string {
	if o.ImageMountPoint != "" {
		return o.ImageMountPoint
	}
	return DefaultMountPoint
}

func (o ExportOptions) imageDevice() string {
	if o.ImageDevice != "" {
		return o.ImageDevice
	}
	return DefaultImageDevice
}

// validateImageSource checks what the archive is compared against, which decides
// what ends up in it: a comparison against something that is not the image the
// sandbox booted from reports the whole filesystem as added, and an archive of
// the whole filesystem carries files the sandbox's own file API would refuse to
// read. The request reaches this API from inside the sandbox, so neither field is
// trusted:
//   - the mount point must actually be a mount, which an empty directory the
//     workload created is not;
//   - the device must be a block device under /dev, not a file the workload
//     wrote and can therefore choose the contents of.
func (o ExportOptions) validateImageSource() error {
	// A test compares two plain directories, deliberately, and neither of these
	// is a mount or a device.
	if o.root != "" {
		return nil
	}

	if o.ImageMountPoint != "" {
		if !filepath.IsAbs(o.ImageMountPoint) || o.ImageMountPoint != filepath.Clean(o.ImageMountPoint) {
			return invalidOptions("imageMountPoint must be an absolute, clean path")
		}
		// Only two comparison sources make sense, and any other one is a way to
		// choose what the archive carries: compared against a filesystem that is
		// not the image - /proc, /dev, an attached drive - every path looks added,
		// so the archive becomes a copy of the whole root, including the files the
		// sandbox's file API refuses to read, streamed to a URL the caller chose.
		//   - this API's own mountpoint, where the image is or was mounted, which is
		//     how a caller reuses a mount instead of paying for another one;
		//   - the root itself, which reports what a comparison against an identical
		//     filesystem reports: nothing.
		if o.ImageMountPoint != DefaultMountPoint && o.ImageMountPoint != DefaultRoot {
			return invalidOptions("imageMountPoint must be %s or %s", DefaultMountPoint, DefaultRoot)
		}
		if !mounted(o.ImageMountPoint) {
			return invalidOptions("imageMountPoint %s is not a mount point", o.ImageMountPoint)
		}
		// Being a mount is not being the image: the workload can mount a drive of
		// its own at this API's mountpoint, and a comparison against it reports
		// every path as added. Only the root is taken on trust, since it is the
		// filesystem being archived and nothing can be substituted for it.
		if o.ImageMountPoint != DefaultRoot && !mountedFromImage(o.ImageMountPoint) {
			return invalidOptions("imageMountPoint %s does not hold the sandbox image", o.ImageMountPoint)
		}
	}

	if o.ImageDevice != "" {
		device := filepath.Clean(o.ImageDevice)
		if device != o.ImageDevice || !strings.HasPrefix(device, "/dev/") {
			return invalidOptions("imageDevice must be a path under /dev")
		}
		// A device under /dev is not enough, for the reason the mount point is
		// restricted to two paths: any other mountable filesystem - an attached
		// drive, an image the workload built - shares nothing with the root, so
		// every path looks added and the archive becomes a copy of the whole root.
		// The image only ever lives on the devices the generations attach it to.
		if !slices.Contains(imageDevices, device) {
			return invalidOptions("imageDevice must be one of %s", strings.Join(imageDevices, ", "))
		}
		info, err := os.Stat(device)
		if err != nil {
			return invalidOptions("image device %s is not available: %v", device, err)
		}
		if info.Mode()&os.ModeDevice == 0 {
			return invalidOptions("imageDevice %s is not a device", device)
		}
	}
	return nil
}

// Export archives the sandbox's filesystem changes to the presigned URL.
//
// A real export quiesces the sandbox first and never lifts the freeze: the
// archive describes a filesystem nothing was writing to, and letting the
// workload resume afterwards would produce a sandbox whose live state has
// silently diverged from the archive it just took. Call Resume to lift it
// deliberately, for instance after a failure.
func Export(ctx context.Context, options ExportOptions) (result *ExportResult, err error) {
	if options.URL == "" && options.Multipart == nil && !options.DryRun {
		return nil, ErrURLRequired
	}
	if err = options.Multipart.validate(); err != nil {
		return nil, err
	}
	if err = options.validateImageSource(); err != nil {
		return nil, err
	}

	// Dry runs and real exports alike: they all mount the pristine image at the
	// same point, and the freeze only holds back the real ones. A second export
	// replacing that mount while the first is reading it compares the filesystem
	// against nothing, so the loser waits for another time rather than making
	// both archives wrong.
	if !exportMu.TryLock() {
		return nil, ErrExportInProgress
	}
	defer exportMu.Unlock()

	started := time.Now()
	result = &ExportResult{}

	if !options.DryRun {
		// Stopping the workload is what makes the sandbox look idle to the
		// infrastructure: the processes holding the keep-alive are gone, and an
		// export triggered from inside the sandbox is not an inbound connection
		// either. A large archive takes longer than the idle deadline, so without
		// this the VM is hibernated in the middle of the upload and the export
		// dies with the sandbox.
		defer blaxel.HoldAwake("the archive export")()

		// Frozen and claimed at once: a resume between the two would lift the
		// freeze the export relies on, and the filesystem would be read while
		// the API serves mutating calls again. From here a resume is refused
		// until the export is done.
		if err = freezeForExport("archive export"); err != nil {
			return nil, err
		}
		defer endExport()
		// The freeze only earns its keep when there is an archive to protect: a
		// failed export must not leave the sandbox locked out with no archive
		// and no way back.
		defer func() {
			if err != nil {
				forceResume()
			}
		}()

		if result.StoppedProcesses, err = quiesceWorkload(options); err != nil {
			return nil, err
		}
	}

	mountPoint := options.imageMountPoint()
	if options.ImageMountPoint == "" {
		// A mount already sitting at this API's own mountpoint is the leftover of
		// an export that could not unmount it, or anything the workload mounted
		// there. It is replaced rather than reused: what it holds is unknown, and
		// comparing against the wrong filesystem silently produces a wrong archive.
		if mounted(mountPoint) {
			logrus.WithField("mountPoint", mountPoint).Warn("[Archive] Replacing a leftover image mount")
			if err = unmountImage(mountPoint); err != nil {
				return nil, err
			}
		}
		if err = mountImage(options.imageDevice(), mountPoint); err != nil {
			return nil, err
		}
		defer func() {
			if err := unmountImage(mountPoint); err != nil {
				logrus.WithError(err).Warn("[Archive] Failed to unmount the image")
			}
		}()
	}

	// The last step before the filesystem is read, and it cannot come earlier:
	// mounting the image creates a directory under /mnt, which is part of the
	// root, and quiescing writes too - the process list is saved to disk and the
	// stopped processes keep writing their logs until they exit. Until here the
	// freeze is the API refusing the routes that write.
	if !options.DryRun {
		completeQuiesce(result.StoppedProcesses, freezeRoot(options))
	}

	excludes := append(append([]string(nil), DefaultExcludes...), options.Excludes...)
	excludes = append(excludes, executableExcludes()...)
	// /mnt is excluded already, but the tests mount nothing and compare two
	// directories of their own, so exclude the mountpoint explicitly too.
	if rel, err := filepath.Rel(options.rootDir(), mountPoint); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		excludes = append(excludes, rel)
	}

	changes, err := Diff(options.rootDir(), mountPoint, excludes)
	if err != nil {
		return nil, err
	}

	manifest := Manifest{
		Version:     ManifestVersion,
		CreatedAt:   time.Now(),
		APIVersion:  APIVersion,
		ImageDevice: options.imageDevice(),
		Root:        options.rootDir(),
		Excludes:    excludes,
	}
	for _, change := range changes {
		switch change.Kind {
		case ChangeAdded:
			manifest.Added++
			manifest.PayloadBytes += change.Size
		case ChangeModified:
			manifest.Modified++
			manifest.PayloadBytes += change.Size
		case ChangeDeleted:
			manifest.Deleted = append(manifest.Deleted, change.Path)
		}
	}

	var metadata []member
	if options.saveProcesses() {
		var processes []byte
		if processes, err = processState(); err != nil {
			return nil, err
		}
		manifest.Processes = true
		metadata = append(metadata, member{name: ProcessesName, data: processes})
	}

	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize the manifest: %w", err)
	}

	metadata = append([]member{{name: ManifestName, data: manifestJSON}}, metadata...)

	// S3 needs a Content-Length: a presigned PUT rejects a chunked body, and the
	// archive is far too large to buffer. Size it first by writing the same
	// archive with the file contents replaced by zeros, which reads no content
	// and produces the exact same layout, then stream it for real.
	size, err := writeArchive(io.Discard, options.rootDir(), metadata, changes, false)
	if err != nil {
		return nil, err
	}

	result.Manifest = manifest
	result.Size = size
	result.Duration = time.Since(started).String()
	if options.DryRun {
		result.Changes = changes
		return result, nil
	}

	write := func(w io.Writer) error {
		_, err := writeArchive(w, options.rootDir(), metadata, changes, true)
		return err
	}
	if options.Multipart != nil {
		// The storage class and anything else the caller signed belongs to the
		// upload it created, not to the parts, so the headers are not sent here.
		err = uploadMultipart(ctx, options.Multipart, size, write)
	} else {
		err = upload(ctx, options.URL, options.Headers, size, write)
	}
	if err != nil {
		return nil, err
	}

	result.Uploaded = true
	result.Duration = time.Since(started).String()
	logrus.WithFields(logrus.Fields{
		"added":    manifest.Added,
		"modified": manifest.Modified,
		"deleted":  len(manifest.Deleted),
		"bytes":    size,
		"duration": result.Duration,
	}).Info("[Archive] Export complete")
	return result, nil
}

// quiesceWorkload saves the process list, then stops every running process and
// waits for them to be gone, so nothing writes to the filesystem while it is
// read. The state is saved before anything is stopped: it has to describe the
// workload as it was, not as a list of stopped processes.
func quiesceWorkload(options ExportOptions) ([]string, error) {
	pm := process.GetProcessManager()

	if options.saveProcesses() {
		if err := pm.SaveState(); err != nil {
			return nil, fmt.Errorf("failed to save the process list: %w", err)
		}
	}

	var stopped []stoppedProcess
	for _, info := range pm.ListProcesses() {
		if info.Status != process.StatusRunning {
			continue
		}
		identifier := info.PID
		candidate := stoppedProcess{
			identifier: identifier,
			pid:        info.ProcessPid,
			done:       info.Done,
			kill:       func() error { return pm.KillProcess(identifier) },
		}
		// A process that could not be asked to stop is kept in the list rather
		// than dropped: it is still running, so it is precisely the one that must
		// go through the wait-and-kill path below instead of being left to write
		// into the archive.
		if err := pm.StopProcess(identifier); err != nil {
			logrus.WithError(err).WithField("process", identifier).Warn("[Archive] Failed to stop process gracefully, it will be killed")
		}
		stopped = append(stopped, candidate)
	}

	if candidate, running := stopStartupWorkload(); running {
		stopped = append(stopped, candidate)
	}

	// A process that ignores SIGTERM would keep writing during the scan, which is
	// exactly what the archive must not contain, so the ones still alive at the
	// deadline are killed.
	if pending := waitForExit(stopped, options.stopTimeout()); len(pending) > 0 {
		for _, candidate := range pending {
			if err := candidate.kill(); err != nil {
				logrus.WithError(err).WithField("process", candidate.identifier).Warn("[Archive] Failed to kill process")
			}
		}
		// SIGKILL cannot be ignored, but the exit still has to have happened
		// before the filesystem is read.
		if alive := waitForExit(pending, killTimeout); len(alive) > 0 {
			logrus.WithField("processes", identifiers(alive)).Error("[Archive] Processes survived SIGKILL: the archive may not be consistent")
		}
	}

	// Flush what the stopped processes wrote: their pages are on tmpfs, but the
	// log files this API keeps for them are not necessarily written back yet.
	syncFilesystem()
	return identifiers(stopped), nil
}

// freezeRoot makes the root read-only and reports whether it worked. Stopping
// the processes and refusing the mutating routes leaves ways to write to the
// filesystem — a terminal session, a request already in flight, a process that
// outlived its stop — and the read-only mount is what closes them, so a failure
// is loud. It is not fatal: an archive of a filesystem nobody is writing to is
// still worth having, and refusing to export because the remount failed would
// leave the sandbox with no way out.
func freezeRoot(options ExportOptions) bool {
	// A test compares two directories rather than a live root, and remounting
	// the machine it runs on read-only is not what it asked for.
	if options.root != "" {
		return false
	}
	if err := setRootReadOnly(options.rootDir(), true); err != nil {
		logrus.WithError(err).Error("[Archive] Failed to remount the root read-only: the archive may not be consistent if anything writes to it")
		return false
	}
	return true
}

// stoppedProcess is a process the export asked to stop, captured before the stop
// so the wait below has the OS process it has to outlive.
type stoppedProcess struct {
	identifier string
	pid        int
	done       chan struct{}
	// kill sends SIGKILL, through the process manager for a process it owns and
	// directly otherwise.
	kill func() error
}

// exited reports whether the OS process is gone. The manager's own status says
// nothing about it: StopProcess marks a process stopped as soon as SIGTERM is
// sent, while the process keeps running — and writing — until it decides to
// exit. The completion channel is the authoritative answer when the manager
// owns the process; the signal probe covers a process it adopted and does not
// wait on.
func (p stoppedProcess) exited() bool {
	if p.done != nil {
		select {
		case <-p.done:
			return true
		default:
		}
	}
	return !processAlive(p.pid)
}

// waitForExit waits up to timeout for the processes to be gone and returns
// those still alive.
func waitForExit(processes []stoppedProcess, timeout time.Duration) []stoppedProcess {
	deadline := time.Now().Add(timeout)
	for {
		var pending []stoppedProcess
		for _, candidate := range processes {
			if !candidate.exited() {
				pending = append(pending, candidate)
			}
		}
		if len(pending) == 0 || time.Now().After(deadline) {
			return pending
		}
		time.Sleep(processPollInterval)
	}
}

// processAlive reports whether an OS process still exists. A zombie answers
// too, which only matters for a process the manager does not wait on: for the
// ones it owns, exited() sees the completion channel first.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func identifiers(processes []stoppedProcess) []string {
	if len(processes) == 0 {
		return nil
	}
	list := make([]string, 0, len(processes))
	for _, candidate := range processes {
		list = append(list, candidate.identifier)
	}
	return list
}

// processState reads back the state file the process manager just wrote, so the
// archive carries exactly what a restore has to load.
func processState() ([]byte, error) {
	data, err := os.ReadFile(process.GetStateFilePath())
	if os.IsNotExist(err) {
		return []byte("{}"), nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the process state: %w", err)
	}
	return data, nil
}

// upload streams the archive to a presigned URL with a known length.
func upload(ctx context.Context, url string, headers map[string]string, size int64, write func(io.Writer) error) error {
	// The export holds the sandbox frozen until it returns, so the upload is not
	// allowed to hang on storage indefinitely.
	ctx, cancel := context.WithTimeout(ctx, transferTimeout)
	defer cancel()

	reader, writer := io.Pipe()
	go func() {
		err := write(writer)
		// Closing with the error makes the request fail rather than upload a
		// truncated archive that S3 would happily accept as complete.
		_ = writer.CloseWithError(err)
	}()
	defer reader.Close()

	request, err := http.NewRequestWithContext(ctx, http.MethodPut, url, reader)
	if err != nil {
		return fmt.Errorf("failed to build the upload request: %w", err)
	}
	request.ContentLength = size
	// No Content-Type by default: a presigned URL signs the headers it was
	// generated for, and a signature computed without a content type does not
	// match a request that sends one (S3 answers SignatureDoesNotMatch). The
	// caller passes the headers it signed, if any.
	for name, value := range headers {
		// Content-Length and Host are the request's own, and letting a caller set
		// them here would either be ignored or corrupt the request.
		switch http.CanonicalHeaderKey(name) {
		case "Content-Length", "Host":
			continue
		}
		request.Header.Set(name, value)
	}

	response, err := transferClient.Do(request)
	if err != nil {
		return fmt.Errorf("failed to upload the archive: %w", redactURL(err))
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		// The URL carries credentials, so it is the status and the storage
		// response that are reported, never the request.
		return fmt.Errorf("archive upload rejected with status %d: %s", response.StatusCode, string(body))
	}
	return nil
}
