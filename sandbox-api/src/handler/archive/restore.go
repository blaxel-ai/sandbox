package archive

import (
	"errors"
	"io"
	"os"
	"sync"
	"time"
)

// RestoreState is where an import has got to.
//
// A restore of a large archive takes minutes, during which the sandbox exists
// and answers but carries neither the image's filesystem nor the archive's.
// Reporting "deployed" through all of it is what leaves a caller waiting on a
// sandbox that looks ready and is not, so every phase is named here.
type RestoreState string

const (
	// RestoreDownloading means the archive is being read from the storage. It
	// is also the phase the files are written in: the archive is applied as it
	// is downloaded, so the two overlap and Downloaded is the progress of both.
	RestoreDownloading RestoreState = "downloading"
	// RestoreExtracting means the archive is being applied to the filesystem.
	RestoreExtracting RestoreState = "extracting"
	// RestoreRelaunching means the filesystem carries the archive and the
	// processes it recorded are being started again.
	RestoreRelaunching RestoreState = "relaunching"
	// RestoreSucceeded means the sandbox is the archived one again.
	RestoreSucceeded RestoreState = "succeeded"
	// RestoreFailed means the archive was not restored. Whether the sandbox is
	// usable is the freeze's business, not this one's: an import that failed
	// after writing leaves it quarantined.
	RestoreFailed RestoreState = "failed"
)

// RestoreProgress reports the import this sandbox was started with.
//
// It is what a client polls instead of guessing from a sandbox that answers
// while its filesystem is still being written.
type RestoreProgress struct {
	State      RestoreState `json:"state" example:"extracting"`
	StartedAt  time.Time    `json:"startedAt"`
	FinishedAt *time.Time   `json:"finishedAt,omitempty"`
	// Size is the archive's size as the storage announced it, and zero when it
	// announced none - a client showing a percentage has to allow for that.
	Size int64 `json:"size,omitempty" example:"3074211"`
	// Downloaded is how much of the archive has been read so far.
	Downloaded int64 `json:"downloaded" example:"1048576"`
	// Restored and Deleted count what the archive changed on the filesystem.
	Restored int `json:"restored" example:"1204"`
	Deleted  int `json:"deleted" example:"3"`
	// Error is why the restore failed, without the presigned URL it used.
	Error string `json:"error,omitempty"`
} // @name RestoreProgress

var (
	restoreMu       sync.Mutex
	restoreProgress *RestoreProgress
)

// PendingImport reports whether this sandbox was started with an archive to
// restore. It is what tells a boot to serve the API before the workload, rather
// than the other way around.
func PendingImport() bool {
	return os.Getenv(EnvImportURL) != ""
}

// beginRestore records a restore as started and makes the API refuse the calls
// that would write to the filesystem while it is being written.
//
// The root is not remounted read-only, unlike every other freeze: the import
// writes to that filesystem. The freeze is what keeps everyone else from
// writing to a filesystem halfway between two states - a terminal is still
// served, since watching the restore is the reason to open one.
func beginRestore(reason string) {
	now := time.Now()

	restoreMu.Lock()
	restoreProgress = &RestoreProgress{State: RestoreDownloading, StartedAt: now}
	restoreMu.Unlock()

	quiesceMu.Lock()
	defer quiesceMu.Unlock()
	if quiesceStatus.State != StateActive {
		// Something already froze the sandbox - an interrupted archive adopted
		// at startup. It knows more about this filesystem than the restore
		// does, and the import will refuse to run anyway.
		return
	}
	quiesceStatus = QuiesceStatus{State: StateRestoring, Reason: reason, Since: &now}
}

// endRestore records how the import ended and gives the sandbox back, unless
// something else froze it in the meantime: a partial import quarantines the
// sandbox, and lifting that here would hand back a filesystem nothing may
// write to.
func endRestore(result *ImportResult, err error) {
	finished := time.Now()

	restoreMu.Lock()
	if restoreProgress != nil {
		restoreProgress.FinishedAt = &finished
		if err != nil {
			restoreProgress.State = RestoreFailed
			restoreProgress.Error = err.Error()
		} else {
			restoreProgress.State = RestoreSucceeded
			if result != nil {
				restoreProgress.Restored = result.Restored
				restoreProgress.Deleted = result.Deleted
			}
		}
	}
	restoreMu.Unlock()

	if errors.Is(err, ErrPartialImport) {
		// The filesystem is neither the image's nor the archive's. The freeze
		// stays until the caller quarantines it: giving the sandbox back here,
		// even for the moment in between, is enough for a client to start
		// writing on top of a state that never existed.
		return
	}

	quiesceMu.Lock()
	defer quiesceMu.Unlock()
	if quiesceStatus.State == StateRestoring {
		quiesceStatus = QuiesceStatus{State: StateActive}
	}
}

// restoring reports the import of this boot, or nothing when the sandbox was
// not started from an archive.
func restoring() *RestoreProgress {
	restoreMu.Lock()
	defer restoreMu.Unlock()
	if restoreProgress == nil {
		return nil
	}
	progress := *restoreProgress
	return &progress
}

// enterRestoreState moves the restore to a phase, leaving a finished one alone.
func enterRestoreState(state RestoreState) {
	restoreMu.Lock()
	defer restoreMu.Unlock()
	if restoreProgress == nil || restoreProgress.FinishedAt != nil {
		return
	}
	restoreProgress.State = state
}

// recordArchiveSize records how big the storage says the archive is, which is
// what turns the byte counter below into a percentage.
func recordArchiveSize(size int64) {
	if size <= 0 {
		return
	}
	restoreMu.Lock()
	defer restoreMu.Unlock()
	if restoreProgress != nil {
		restoreProgress.Size = size
	}
}

// countingReader reports how much of the archive has been read. The archive is
// applied as it is downloaded, so this counter is the progress of the whole
// restore and not only of its transfer.
type countingReader struct {
	reader io.Reader
	read   int64
	// flush bounds how often the shared progress is updated: a tar of a
	// filesystem is read in tens of thousands of small chunks, and taking a
	// lock for each of them would cost more than the restore.
	flushed int64
}

const restoreProgressFlush = 4 << 20

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	if n > 0 {
		c.read += int64(n)
		if c.read-c.flushed >= restoreProgressFlush || err != nil {
			c.flush()
		}
	}
	if err != nil {
		c.flush()
	}
	return n, err
}

func (c *countingReader) flush() {
	c.flushed = c.read

	restoreMu.Lock()
	defer restoreMu.Unlock()
	if restoreProgress != nil {
		restoreProgress.Downloaded = c.read
	}
}
