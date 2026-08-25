package archive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blaxel-ai/sandbox-api/src/handler/process"
	"github.com/sirupsen/logrus"
)

const (
	// DefaultMountPoint is where the pristine image is mounted for comparison.
	// It is under /mnt, a tmpfs, so it never appears in the diff itself.
	DefaultMountPoint = "/mnt/blaxel-archive-image"
	// DefaultRoot is the filesystem archived.
	DefaultRoot = "/"
	// DefaultStopTimeout is how long the workload is given to exit after
	// SIGTERM before it is killed.
	DefaultStopTimeout = 30 * time.Second
)

// APIVersion is the sandbox-api build recorded in the manifests it produces.
// Set from the handler, which owns the build information.
var APIVersion = "dev"

// ErrURLRequired is returned for an export with nowhere to upload to, which is
// a bad request rather than a failed export.
var ErrURLRequired = errors.New("url is required unless dryRun is set")

// ExportOptions drives an export.
type ExportOptions struct {
	// URL is a presigned S3 PUT URL the archive is streamed to. Empty is only
	// valid with DryRun.
	URL string `json:"url,omitempty" example:"https://bucket.s3.amazonaws.com/key?..."`
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

// Export archives the sandbox's filesystem changes to the presigned URL.
//
// A real export quiesces the sandbox first and never lifts the freeze: the
// archive describes a filesystem nothing was writing to, and letting the
// workload resume afterwards would produce a sandbox whose live state has
// silently diverged from the archive it just took. Call Resume to lift it
// deliberately, for instance after a failure.
func Export(ctx context.Context, options ExportOptions) (result *ExportResult, err error) {
	if options.URL == "" && !options.DryRun {
		return nil, ErrURLRequired
	}

	started := time.Now()
	result = &ExportResult{}

	if !options.DryRun {
		if err = Freeze("archive export"); err != nil {
			return nil, err
		}
		// The freeze only earns its keep when there is an archive to protect: a
		// failed export must not leave the sandbox locked out with no archive
		// and no way back.
		defer func() {
			if err != nil {
				Resume()
			}
		}()

		if result.StoppedProcesses, err = quiesceWorkload(options); err != nil {
			return nil, err
		}
	}

	mountPoint := options.imageMountPoint()
	if options.ImageMountPoint == "" && !mounted(mountPoint) {
		if err = mountImage(options.imageDevice(), mountPoint); err != nil {
			return nil, err
		}
		defer func() {
			if err := unmountImage(mountPoint); err != nil {
				logrus.WithError(err).Warn("[Archive] Failed to unmount the image")
			}
		}()
	}

	// After the image is mounted, since mounting it needs a mountpoint to be
	// created, and before the filesystem is read.
	if !options.DryRun {
		completeQuiesce(result.StoppedProcesses, freezeRoot(options))
	}

	excludes := append(append([]string(nil), DefaultExcludes...), options.Excludes...)
	// The comparison mount is on tmpfs and so already off the root device, but
	// exclude it explicitly for the tests, which run entirely on one device.
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

	if err = upload(ctx, options.URL, size, func(w io.Writer) error {
		_, err := writeArchive(w, options.rootDir(), metadata, changes, true)
		return err
	}); err != nil {
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

	var stopped []string
	for _, info := range pm.ListProcesses() {
		if info.Status != process.StatusRunning {
			continue
		}
		if err := pm.StopProcess(info.PID); err != nil {
			logrus.WithError(err).WithField("process", info.PID).Warn("[Archive] Failed to stop process")
			continue
		}
		stopped = append(stopped, info.PID)
	}

	deadline := time.Now().Add(options.stopTimeout())
	for {
		running := runningProcesses(pm)
		if len(running) == 0 {
			break
		}
		if time.Now().After(deadline) {
			// A process that ignores SIGTERM would keep writing during the scan,
			// which is exactly what the archive must not contain.
			for _, identifier := range running {
				if err := pm.KillProcess(identifier); err != nil {
					logrus.WithError(err).WithField("process", identifier).Warn("[Archive] Failed to kill process")
				}
			}
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Flush what the stopped processes wrote: their pages are on tmpfs, but the
	// log files this API keeps for them are not necessarily written back yet.
	syncFilesystem()
	return stopped, nil
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

func runningProcesses(pm *process.ProcessManager) []string {
	var running []string
	for _, info := range pm.ListProcesses() {
		if info.Status == process.StatusRunning {
			running = append(running, info.PID)
		}
	}
	return running
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
func upload(ctx context.Context, url string, size int64, write func(io.Writer) error) error {
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
	// No Content-Type: a presigned URL signs the headers it was generated for,
	// and a signature computed without a content type does not match a request
	// that sends one (S3 answers SignatureDoesNotMatch).

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("failed to upload the archive: %w", err)
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
