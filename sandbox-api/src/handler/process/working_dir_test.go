package process

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInheritedWorkingDirSurvivesRestoreAndRefreshesOnRestart(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	originalLogDir := ProcessLogDir
	root := t.TempDir()
	ProcessLogDir = filepath.Join(root, "logs")
	t.Setenv("SANDBOX_STATE_FILE", filepath.Join(root, "state.json"))
	t.Cleanup(func() { _ = os.Chdir(original); ProcessLogDir = originalLogDir })
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, dir := range []string{first, second} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chdir(first); err != nil {
		t.Fatal(err)
	}
	pm := NewProcessManager()
	pid, err := pm.StartProcess("pwd", "", nil, false, 0, false, 0, false, func(*ProcessInfo) {})
	if err != nil {
		t.Fatal(err)
	}
	proc, _ := pm.GetProcessByIdentifier(pid)
	select {
	case <-proc.Finished:
	case <-time.After(5 * time.Second):
		t.Fatal("process did not finish")
	}
	if proc.WorkingDir != "" {
		t.Fatal("inherited cwd became explicit spawn configuration")
	}
	if err := pm.SaveState(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(second); err != nil {
		t.Fatal(err)
	}
	restored := NewProcessManager()
	if err := restored.LoadState(); err != nil {
		t.Fatal(err)
	}
	snapshot, ok := restored.GetProcessSnapshot(pid)
	if !ok || snapshot.WorkingDir != first {
		t.Fatalf("restored cwd = %q, want %q", snapshot.WorkingDir, first)
	}
	if output := snapshot.OutputTail(1024); strings.TrimSpace(output.Stdout) != first {
		t.Fatalf("pwd output = %q", output.Stdout)
	}
	restoredProc, _ := restored.GetProcessByIdentifier(pid)
	if restoredProc.WorkingDir != "" {
		t.Fatal("restoration changed spawn configuration")
	}
	restartFinished := make(chan struct{})
	if _, err := restored.restartProcess(restoredProc, func(*ProcessInfo) { close(restartFinished) }); err != nil {
		t.Fatal(err)
	}
	select {
	case <-restartFinished:
	case <-time.After(5 * time.Second):
		t.Fatal("restart did not finish")
	}
	snapshot, _ = restored.GetProcessSnapshot(pid)
	if snapshot.WorkingDir != second {
		t.Fatalf("restart cwd = %q, want %q", snapshot.WorkingDir, second)
	}
}
