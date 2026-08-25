// Package archive exports the sandbox's filesystem changes so the sandbox can
// be destroyed and recreated later from the same base image.
//
// The root filesystem is an overlay whose lower layer is the read-only EROFS
// image the sandbox booted from and whose upper layer is a tmpfs, i.e. guest
// RAM. Everything the workload changed therefore lives in RAM and dies with the
// VM; an archive is the difference between the live merged root and that
// pristine image, which is small compared to the image itself.
//
// Guest memory is not preserved, so an export first quiesces the sandbox: the
// process list is saved (to be relaunched on the other side), the processes are
// stopped, the root mount is remounted read-only so nothing can write to the
// filesystem while it is read, and the API refuses the calls that would try to.
// See Freeze and Export.
package archive

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/blaxel-ai/sandbox-api/src/handler/process"
	"github.com/sirupsen/logrus"
)

// QuiesceState is the lifecycle of the sandbox with respect to archiving.
type QuiesceState string

const (
	// StateActive is the normal state: the API serves every route.
	StateActive QuiesceState = "active"
	// StateQuiescing means the processes are being stopped.
	StateQuiescing QuiesceState = "quiescing"
	// StateQuiesced means the sandbox is frozen: the workload is stopped and
	// the mutating routes are refused. The filesystem can be read consistently.
	StateQuiesced QuiesceState = "quiesced"
	// StateRestoring means an archive is being written over this sandbox's
	// filesystem. The mutating routes are refused like they are for an export -
	// the filesystem is halfway between the image and the archive, and a caller
	// writing into it would be building on a state that never existed - but the
	// root stays writable, since the import is what writes to it.
	StateRestoring QuiesceState = "restoring"
)

// QuiesceStatus reports the quiesce lifecycle.
type QuiesceStatus struct {
	State QuiesceState `json:"state" binding:"required" example:"quiesced"`
	// Reason is a human readable explanation of why the sandbox is frozen.
	Reason string `json:"reason,omitempty" example:"archive export"`
	// Since is when the sandbox left StateActive.
	Since *time.Time `json:"since,omitempty"`
	// StoppedProcesses are the process identifiers stopped while quiescing.
	StoppedProcesses []string `json:"stoppedProcesses,omitempty"`
	// ReadOnlyRoot reports whether the root mount was remounted read-only, which
	// is what actually stops writes; false means the freeze relies only on the
	// API refusing calls, and the reason says why.
	ReadOnlyRoot bool `json:"readOnlyRoot" example:"true"`
	// Export reports the export started asynchronously, if there was one. It is
	// how a caller that did not wait for the export learns that the archive is
	// on the storage, or why it is not.
	Export *ExportProgress `json:"export,omitempty"`
	// Restore reports the archive this sandbox was started from, if it was
	// started from one: how far its restore has got, and how it ended.
	Restore *RestoreProgress `json:"restore,omitempty"`
} // @name QuiesceStatus

var (
	quiesceMu     sync.RWMutex
	quiesceStatus = QuiesceStatus{State: StateActive}
	// exporting is set while an export reads and uploads the filesystem, so a
	// resume cannot make the root writable underneath it.
	exporting bool
	// allowRestarts lifts the restart suspension the freeze took, so a resumed
	// sandbox keeps restarting its failed processes as it did before.
	allowRestarts func()
)

// ErrExportInProgress is returned by Resume while an export is still reading the
// filesystem, which is the one moment the freeze must not be lifted.
var ErrExportInProgress = errors.New("an archive export is in progress")

// ErrRootReadOnly is returned by Resume when the freeze was lifted on the API
// but the root filesystem could not be made writable again, so the sandbox is
// still not usable and says so rather than reporting itself active.
var ErrRootReadOnly = errors.New("the root filesystem is still read-only")

// ErrRestoreInProgress is returned by Resume while an archive is being written
// to the filesystem: lifting the freeze then would let the workload write into
// a filesystem the import is still building.
var ErrRestoreInProgress = errors.New("an archive is being restored")

// ErrAlreadyQuiesced is returned by Freeze when the sandbox is already frozen,
// so a second export is reported as the conflict it is rather than as a failure
// of the export itself.
var ErrAlreadyQuiesced = errors.New("sandbox is already frozen")

// Quiesced reports whether the sandbox currently refuses mutating calls.
func Quiesced() bool {
	quiesceMu.RLock()
	defer quiesceMu.RUnlock()
	return quiesceStatus.State != StateActive
}

// Status returns a copy of the current quiesce status.
func Status() QuiesceStatus {
	quiesceMu.RLock()
	defer quiesceMu.RUnlock()
	return statusLocked()
}

func statusLocked() QuiesceStatus {
	status := quiesceStatus
	status.StoppedProcesses = append([]string(nil), quiesceStatus.StoppedProcesses...)
	status.Export = exportProgress()
	status.Restore = restoring()
	return status
}

// Freeze moves the sandbox out of StateActive, which makes the API refuse the
// calls that would write to the filesystem. It fails if an export is already in
// progress, so two concurrent exports cannot read a filesystem the other one is
// still settling.
func Freeze(reason string) error {
	quiesceMu.Lock()
	defer quiesceMu.Unlock()
	return freezeLocked(reason)
}

// freezeForExport freezes the sandbox and claims the filesystem for the export
// in one step. Freezing and claiming separately leaves a window a resume fits
// into: it would find no export in progress, lift the freeze the export is
// about to rely on, and the export would then read a filesystem the API is
// serving mutating calls on again.
func freezeForExport(reason string) error {
	quiesceMu.Lock()
	defer quiesceMu.Unlock()
	if err := freezeLocked(reason); err != nil {
		return err
	}
	exporting = true
	return nil
}

func freezeLocked(reason string) error {
	if quiesceStatus.State != StateActive {
		return fmt.Errorf("%w: %s (%s)", ErrAlreadyQuiesced, quiesceStatus.State, quiesceStatus.Reason)
	}
	// Before anything is stopped: a process that failed just before the freeze is
	// waiting to be restarted, and it would come back after the export listed the
	// processes it had to stop - writing to the filesystem being archived, under a
	// name the export never saw running.
	allowRestarts = process.SuspendRestarts()
	now := time.Now()
	quiesceStatus = QuiesceStatus{
		State:  StateQuiescing,
		Reason: reason,
		Since:  &now,
	}
	return nil
}

// AdoptRootState makes the quiesce status say what the filesystem actually is,
// at startup. The status is process memory and the read-only root is a mount, so
// a restart in the middle of an archive - a crash, an upgrade, the API being
// killed - would otherwise come back reporting itself active on a filesystem
// where every write fails, and Resume would not even try to remount it, having
// no memory of a freeze.
//
// The sandbox is reported quiesced instead, which is both true and what Resume
// undoes: an operator gets a usable sandbox back with one call.
func AdoptRootState() {
	adoptRootState(DefaultRoot)
}

func adoptRootState(root string) {
	readOnly, err := rootReadOnly(root)
	if err != nil {
		logrus.WithError(err).Warn("[Archive] Could not tell whether the root filesystem is read-only")
		return
	}
	if !readOnly {
		return
	}
	logrus.Warn("[Archive] The root filesystem is read-only at startup, an archive was interrupted: freezing the sandbox until it is resumed")
	now := time.Now()
	quiesceMu.Lock()
	defer quiesceMu.Unlock()
	allowRestarts = process.SuspendRestarts()
	quiesceStatus = QuiesceStatus{
		State:        StateQuiesced,
		Reason:       "an interrupted archive left the root filesystem read-only",
		Since:        &now,
		ReadOnlyRoot: true,
	}
}

// Quarantine freezes the sandbox on a filesystem nothing may write to any more,
// which is what a half restored archive leaves behind: neither the image's
// filesystem nor the archived sandbox's.
//
// Freeze alone is not enough there. It only makes the API refuse the mutating
// routes, and a route it still serves - a terminal session, a file read through
// a shell - writes to the filesystem all the same. So the root is remounted
// read-only and the sandbox is reported as quiesced, the state Resume undoes,
// leaving an operator a sandbox to look at and nothing that can make its
// filesystem worse.
func Quarantine(reason string) error {
	return quarantine(DefaultRoot, reason)
}

func quarantine(root, reason string) error {
	if err := freezeQuarantined(reason); err != nil {
		return err
	}
	readOnly := true
	if err := setRootReadOnly(root, true); err != nil {
		// Reported rather than returned: the sandbox stays frozen either way,
		// and the status says whether writes are actually stopped.
		logrus.WithError(err).Error("[Archive] Failed to remount the root read-only after a failed import")
		readOnly = false
	}
	completeQuiesce(nil, readOnly)
	return nil
}

// freezeQuarantined freezes a sandbox that may already be frozen for a restore.
// A failed import is exactly that case, and refusing to quarantine it because
// the restore's own freeze is still in place would leave the filesystem
// writable through the routes the restore still serves.
func freezeQuarantined(reason string) error {
	quiesceMu.Lock()
	if quiesceStatus.State == StateRestoring {
		now := time.Now()
		allowRestarts = process.SuspendRestarts()
		quiesceStatus = QuiesceStatus{State: StateQuiescing, Reason: reason, Since: &now}
		quiesceMu.Unlock()
		return nil
	}
	quiesceMu.Unlock()
	return Freeze(reason)
}

// endExport releases the claim freezeForExport took on the filesystem.
func endExport() {
	quiesceMu.Lock()
	defer quiesceMu.Unlock()
	exporting = false
}

// completeQuiesce records that the workload is stopped and the filesystem is
// stable.
func completeQuiesce(stopped []string, readOnlyRoot bool) {
	quiesceMu.Lock()
	defer quiesceMu.Unlock()
	quiesceStatus.State = StateQuiesced
	quiesceStatus.StoppedProcesses = stopped
	quiesceStatus.ReadOnlyRoot = readOnlyRoot
}

// Resume lifts the freeze. The sandbox does not come back to what it was: the
// processes stopped while quiescing are not relaunched, since an exported
// sandbox is meant to be destroyed and restored elsewhere. It exists so a
// failed or aborted export does not leave the API permanently refusing calls.
//
// It refuses to run while an export is still reading the filesystem: lifting the
// freeze then would let the workload write into the archive being produced, and
// the export lifts it itself when it fails.
func Resume() (QuiesceStatus, error) {
	// The claim is read and the freeze lifted under the same lock: releasing it
	// in between leaves a window an export starts in, and this resume would then
	// make the root writable again while the export is already reading it -
	// archiving a filesystem the workload can still change under it.
	quiesceMu.Lock()
	defer quiesceMu.Unlock()
	if exporting {
		return statusLocked(), ErrExportInProgress
	}
	if quiesceStatus.State == StateRestoring {
		return statusLocked(), ErrRestoreInProgress
	}
	status := resumeLocked()
	if status.State != StateActive {
		return status, ErrRootReadOnly
	}
	return status, nil
}

// forceResume lifts the freeze unconditionally. Only an export that is giving up
// may use it: it is the one caller that knows it has stopped reading.
func forceResume() QuiesceStatus {
	quiesceMu.Lock()
	defer quiesceMu.Unlock()
	return resumeLocked()
}

func resumeLocked() QuiesceStatus {
	exporting = false
	if allowRestarts != nil {
		allowRestarts()
		allowRestarts = nil
	}
	if quiesceStatus.ReadOnlyRoot {
		if err := setRootReadOnly(DefaultRoot, false); err != nil {
			// The API would serve every route again while every write still
			// fails, so the sandbox stays frozen and keeps saying that its root
			// is read-only: a status claiming otherwise is worse than one
			// admitting the sandbox needs to be recreated.
			logrus.WithError(err).Error("[Archive] Failed to make the root writable again")
			quiesceStatus.State = StateQuiesced
			quiesceStatus.Reason = "the root filesystem could not be made writable again"
			quiesceStatus.StoppedProcesses = nil
			return quiesceStatus
		}
	}
	quiesceStatus = QuiesceStatus{State: StateActive}
	return quiesceStatus
}
