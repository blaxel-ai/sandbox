package process

import (
	"os"
	"path/filepath"
	"testing"
)

// The default state file lives in /tmp: a temp file another user planted at
// a predictable name must not receive the state, nor become the state file.
func TestSaveStateIgnoresAPlantedTempFile(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "state.json")
	t.Setenv("SANDBOX_STATE_FILE", stateFile)
	planted := stateFile + ".tmp"
	if err := os.WriteFile(planted, []byte("planted"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(planted, 0644); err != nil {
		t.Fatal(err)
	}

	if err := NewProcessManager().SaveState(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("state file mode = %o, want 600", perm)
	}
	if got, err := os.ReadFile(planted); err != nil || string(got) != "planted" {
		t.Fatalf("planted temp file = %q, %v; want it untouched", got, err)
	}
	if leftovers, _ := filepath.Glob(stateFile + ".*.tmp"); len(leftovers) != 0 {
		t.Fatalf("temp files left behind: %v", leftovers)
	}
}
