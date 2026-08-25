// Package archive exports the sandbox's filesystem changes so the sandbox can
// be destroyed and recreated later from the same base image.
//
// The root filesystem is an overlay whose lower layer is the read-only EROFS
// image the sandbox booted from and whose upper layer is a tmpfs, i.e. guest
// RAM. Everything the workload changed therefore lives in RAM and dies with the
// VM; an archive is the difference between the live merged root and that
// pristine image, which is small compared to the image itself.
//
// Guest memory is not preserved, so an export first quiesces the sandbox:
// the process list is saved (to be relaunched on the other side), the
// processes are stopped, and the API stops accepting the calls that would
// write to the filesystem while it is being read. See Freeze and Export.
package archive

import (
	"fmt"
	"sync"
	"time"
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
} // @name QuiesceStatus

var (
	quiesceMu     sync.RWMutex
	quiesceStatus = QuiesceStatus{State: StateActive}
)

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
		return fmt.Errorf("sandbox is already %s (%s)", quiesceStatus.State, quiesceStatus.Reason)
	}
	now := time.Now()
	quiesceStatus = QuiesceStatus{
		State:  StateQuiescing,
		Reason: reason,
		Since:  &now,
	}
	return nil
}

// completeQuiesce records that the workload is stopped and the filesystem is
// stable.
func completeQuiesce(stopped []string) {
	quiesceMu.Lock()
	defer quiesceMu.Unlock()
	quiesceStatus.State = StateQuiesced
	quiesceStatus.StoppedProcesses = stopped
}

// Resume lifts the freeze. The sandbox does not come back to what it was: the
// processes stopped while quiescing are not relaunched, since an exported
// sandbox is meant to be destroyed and restored elsewhere. It exists so a
// failed or aborted export does not leave the API permanently refusing calls.
func Resume() QuiesceStatus {
	quiesceMu.Lock()
	defer quiesceMu.Unlock()
	quiesceStatus = QuiesceStatus{State: StateActive}
	return quiesceStatus
}
