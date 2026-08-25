package archive

import "testing"

func TestQuiesceLifecycle(t *testing.T) {
	t.Cleanup(func() { Resume() })

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

	if status := Resume(); status.State != StateActive {
		t.Errorf("expected the freeze to be lifted, got %+v", status)
	}
	if Quiesced() {
		t.Error("expected the sandbox to serve calls again")
	}
}

func TestStatusIsACopy(t *testing.T) {
	t.Cleanup(func() { Resume() })
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
