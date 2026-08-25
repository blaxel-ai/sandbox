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
} // @name QuiesceStatus

var (
	quiesceMu     sync.RWMutex
	quiesceStatus = QuiesceStatus{State: StateActive}
	// exporting is set while an export reads and uploads the filesystem, so a
	// resume cannot make the root writable underneath it.
	exporting bool
)

// ErrExportInProgress is returned by Resume while an export is still reading the
// filesystem, which is the one moment the freeze must not be lifted.
var ErrExportInProgress = errors.New("an archive export is in progress")

// ErrRootReadOnly is returned by Resume when the freeze was lifted on the API
// but the root filesystem could not be made writable again, so the sandbox is
// still not usable and says so rather than reporting itself active.
var ErrRootReadOnly = errors.New("the root filesystem is still read-only")

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
	status := quiesceStatus
	status.StoppedProcesses = append([]string(nil), quiesceStatus.StoppedProcesses...)
	return status
}

// Freeze moves the sandbox out of StateActive, which makes the API refuse the
// calls that would write to the filesystem. It fails if an export is already in
// progress, so two concurrent exports cannot read a filesystem the other one is
// still settling.
func Freeze(reason string) error {
	quiesceMu.Lock()
	defer quiesceMu.Unlock()
	if quiesceStatus.State != StateActive {
		return fmt.Errorf("%w: %s (%s)", ErrAlreadyQuiesced, quiesceStatus.State, quiesceStatus.Reason)
	}
	now := time.Now()
	quiesceStatus = QuiesceStatus{
		State:  StateQuiescing,
		Reason: reason,
		Since:  &now,
	}
	return nil
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
	if err := Freeze(reason); err != nil {
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

// beginExport marks the export as reading the filesystem. It must be called
// with the sandbox frozen, and its counterpart endExport always called after.
func beginExport() {
	quiesceMu.Lock()
	defer quiesceMu.Unlock()
	exporting = true
}

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
	quiesceMu.RLock()
	inProgress := exporting
	quiesceMu.RUnlock()
	if inProgress {
		return Status(), ErrExportInProgress
	}
	status := forceResume()
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
	exporting = false
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
