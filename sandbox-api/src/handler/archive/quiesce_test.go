package archive

import (
	"errors"
	"testing"
)

func TestQuiesceLifecycle(t *testing.T) {
	t.Cleanup(func() { forceResume() })

	if Quiesced() {
		t.Fatal("a sandbox starts serving every route")
	}
	if err := Freeze("archive export"); err != nil {
		t.Fatalf("failed to freeze: %v", err)
	}
	if !Quiesced() {
		t.Error("expected the sandbox to be frozen as soon as the export starts, not once the processes are stopped")
	}
	if status := Status(); status.State != StateQuiescing || status.Since == nil {
		t.Errorf("unexpected status %+v", status)
	}

	if err := Freeze("another export"); err == nil {
		t.Error("expected a second export to be refused")
	}
	if status := Status(); status.Reason != "archive export" {
		t.Errorf("a refused export must not overwrite the reason, got %q", status.Reason)
	}

	completeQuiesce([]string{"proc-1"}, false)
	if status := Status(); status.State != StateQuiesced || len(status.StoppedProcesses) != 1 {
		t.Errorf("unexpected status %+v", status)
	}

	status, err := Resume()
	if err != nil {
		t.Fatalf("unexpected resume error: %v", err)
	}
	if status.State != StateActive {
		t.Errorf("expected the freeze to be lifted, got %+v", status)
	}
	if Quiesced() {
		t.Error("expected the sandbox to serve calls again")
	}
}

func TestStatusIsACopy(t *testing.T) {
	t.Cleanup(func() { forceResume() })
	if err := Freeze("archive export"); err != nil {
		t.Fatal(err)
	}
	completeQuiesce([]string{"proc-1"}, false)

	status := Status()
	status.StoppedProcesses[0] = "tampered"
	if Status().StoppedProcesses[0] != "proc-1" {
		t.Error("Status must not hand out the internal slice")
	}
}

func TestResumeIsRefusedWhileAnExportReadsTheFilesystem(t *testing.T) {
	t.Cleanup(func() { forceResume() })
	if err := Freeze("archive export"); err != nil {
		t.Fatal(err)
	}
	beginExport()
	completeQuiesce(nil, false)

	if _, err := Resume(); !errors.Is(err, ErrExportInProgress) {
		t.Errorf("expected the resume to be refused, got %v", err)
	}
	if !Quiesced() {
		t.Error("a refused resume must leave the sandbox frozen")
	}

	endExport()
	if _, err := Resume(); err != nil {
		t.Fatalf("the freeze should be liftable once the export is done: %v", err)
	}
}
