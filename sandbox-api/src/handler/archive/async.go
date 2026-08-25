package archive

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// ExportState is the lifecycle of an export the caller did not wait for.
type ExportState string

const (
	// ExportRunning means the filesystem is being read and uploaded.
	ExportRunning ExportState = "running"
	// ExportSucceeded means the archive is on the storage.
	ExportSucceeded ExportState = "succeeded"
	// ExportFailed means the archive was not uploaded. The sandbox resumed
	// itself, since a failed export must not leave it frozen with no archive.
	ExportFailed ExportState = "failed"
)

// ExportProgress reports an export started asynchronously.
//
// An archive of a large filesystem takes far longer than a client is willing to
// hold a request open, so the caller starts the export and follows it here,
// through /archive/status.
type ExportProgress struct {
	State      ExportState `json:"state" example:"running"`
	StartedAt  time.Time   `json:"startedAt"`
	FinishedAt *time.Time  `json:"finishedAt,omitempty"`
	// Size is the archive's exact size, known once the filesystem is scanned.
	Size int64 `json:"size,omitempty" example:"3074211"`
	// Uploaded reports whether the storage holds the archive.
	Uploaded bool `json:"uploaded" example:"false"`
	// Error is why the export failed, without the presigned URL it used.
	Error string `json:"error,omitempty"`
} // @name ExportProgress

var (
	asyncMu       sync.Mutex
	asyncProgress *ExportProgress
)

// StartExport runs an export in the background and reports it as started.
//
// Everything that makes a request wrong - the options, a second export - is
// answered before it returns, so a caller that got a progress back knows the
// export is its own. Everything that takes time, the freeze included, happens
// after: quiescing stops the workload and waits for it to exit, which is
// already too long to hold a request for.
func StartExport(ctx context.Context, options ExportOptions) (ExportProgress, error) {
	if options.DryRun {
		return ExportProgress{}, invalidOptions("a dry run is not run asynchronously: it uploads nothing and answers with what it found")
	}
	if options.URL == "" && options.Multipart == nil {
		return ExportProgress{}, ErrURLRequired
	}
	if err := options.Multipart.validate(); err != nil {
		return ExportProgress{}, err
	}
	if err := options.validateImageSource(); err != nil {
		return ExportProgress{}, err
	}

	asyncMu.Lock()
	defer asyncMu.Unlock()
	if asyncProgress != nil && asyncProgress.State == ExportRunning {
		return *asyncProgress, ErrExportInProgress
	}

	progress := ExportProgress{State: ExportRunning, StartedAt: time.Now()}
	asyncProgress = &progress

	go func() {
		result, err := Export(ctx, options)
		finishExport(result, err)
	}()

	return progress, nil
}

// finishExport records how the background export ended. It is the only lasting
// trace of it: the caller is long gone, and the sandbox is about to be deleted
// or resumed depending on what happened here.
func finishExport(result *ExportResult, err error) {
	finished := time.Now()

	asyncMu.Lock()
	defer asyncMu.Unlock()
	if asyncProgress == nil {
		return
	}
	asyncProgress.FinishedAt = &finished
	if err != nil {
		asyncProgress.State = ExportFailed
		asyncProgress.Error = err.Error()
		logrus.WithError(err).Error("[Archive] Background export failed")
		return
	}
	asyncProgress.State = ExportSucceeded
	asyncProgress.Size = result.Size
	asyncProgress.Uploaded = result.Uploaded
}

// exportProgress is what /archive/status reports about the last export started
// asynchronously, or nothing when there was none.
func exportProgress() *ExportProgress {
	asyncMu.Lock()
	defer asyncMu.Unlock()
	if asyncProgress == nil {
		return nil
	}
	progress := *asyncProgress
	return &progress
}
