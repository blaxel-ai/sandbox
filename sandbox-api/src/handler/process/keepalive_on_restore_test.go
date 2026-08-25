package process

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A sandbox-api restart resets the scale-to-zero counter to 0, wiping the hold
// a keepAlive process took when it started. Adopting the still-running process
// must re-take that hold, or the sandbox hibernates under a live workload; and
// the hold must be released again when the adopted process exits on its own.
func TestAdoptedKeepAliveProcessRetakesScaleHold(t *testing.T) {
	originalLogDir := ProcessLogDir
	dir := t.TempDir()
	ProcessLogDir = dir
	t.Cleanup(func() { ProcessLogDir = originalLogDir })

	stateFile := filepath.Join(dir, "state.json")
	t.Setenv("SANDBOX_STATE_FILE", stateFile)

	scaleFile := filepath.Join(dir, "scale_to_zero_disable")
	t.Setenv("BLAXEL_SCALE_FILE", scaleFile)

	// The previous run's keepAlive process, still alive across the restart.
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting survivor process: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	state := ManagerState{
		Version: 1,
		SavedAt: time.Now(),
		Processes: map[string]ProcessState{
			"1234": {
				PID:        "1234",
				Name:       "survivor",
				Command:    "sleep 60",
				ProcessPid: cmd.Process.Pid,
				StartedAt:  time.Now().Add(-time.Minute),
				Status:     StatusRunning,
				KeepAlive:  true,
			},
		},
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("encoding state: %v", err)
	}
	if err := os.WriteFile(stateFile, encoded, 0o644); err != nil {
		t.Fatalf("writing state file: %v", err)
	}

	pm := NewProcessManager()
	if err := pm.LoadState(); err != nil {
		t.Fatalf("loading state: %v", err)
	}

	proc, ok := pm.GetProcessByIdentifier("survivor")
	if !ok {
		t.Fatal("the process was not adopted")
	}
	if proc.Status != StatusRunning {
		t.Fatalf("expected the adopted process to be running, got %q", proc.Status)
	}

	if content, err := os.ReadFile(scaleFile); err != nil || string(content) != "+" {
		t.Fatalf("expected adoption to re-take the scale-to-zero hold ('+'), got %q (%v)", content, err)
	}

	// When the adopted process exits on its own, the monitor must release the
	// hold it took, or the sandbox never hibernates again.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("killing survivor process: %v", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if content, err := os.ReadFile(scaleFile); err == nil && string(content) == "-" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	content, _ := os.ReadFile(scaleFile)
	t.Fatalf("expected the hold to be released ('-') after the adopted process exited, got %q", content)
}
