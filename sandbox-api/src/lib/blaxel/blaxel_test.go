package blaxel

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestKeepAliveDisabled(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"true", true},
		{"1", true},
		{" true ", true},
		{"false", false},
		{"0", false},
		{"notabool", false},
	}
	for _, c := range cases {
		t.Run("value="+c.value, func(t *testing.T) {
			t.Setenv(EnvDisableKeepAlive, c.value)
			if got := KeepAliveDisabled(); got != c.want {
				t.Errorf("KeepAliveDisabled() with %q = %v, want %v", c.value, got, c.want)
			}
		})
	}
}

func TestHoldAwakeReleasesTheCounterOnce(t *testing.T) {
	scaleFile := filepath.Join(t.TempDir(), "scale_to_zero_disable")
	t.Setenv("BLAXEL_SCALE_FILE", scaleFile)
	// The availability of the scale file is decided once per process.
	scaleAvailableChecked, scaleAvailable = true, true
	t.Cleanup(func() { scaleAvailableChecked, scaleAvailable = false, false })

	// A sandbox that opted out of keeping itself alive for its workload still
	// cannot be hibernated in the middle of an archive.
	t.Setenv(EnvDisableKeepAlive, "true")

	release := HoldAwake("a test")
	if content, err := os.ReadFile(scaleFile); err != nil || string(content) != "+" {
		t.Fatalf("expected the hold to disable scale-to-zero, got %q (%v)", content, err)
	}

	release()
	if content, err := os.ReadFile(scaleFile); err != nil || string(content) != "-" {
		t.Fatalf("expected the release to re-enable scale-to-zero, got %q (%v)", content, err)
	}

	// A second release would decrement a counter another hold is relying on.
	if err := os.WriteFile(scaleFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	release()
	if content, err := os.ReadFile(scaleFile); err != nil || string(content) != "x" {
		t.Errorf("expected releasing twice to write nothing the second time, got %q (%v)", content, err)
	}
}

func TestHoldAwakeDoesNotReleaseAHoldItFailedToTake(t *testing.T) {
	// The scale file lives in a directory that does not exist, so the hold
	// fails; releasing anyway would decrement someone else's hold.
	scaleFile := filepath.Join(t.TempDir(), "missing", "scale_to_zero_disable")
	t.Setenv("BLAXEL_SCALE_FILE", scaleFile)
	scaleAvailableChecked, scaleAvailable = true, true
	t.Cleanup(func() { scaleAvailableChecked, scaleAvailable = false, false })

	HoldAwake("a test")()

	if _, err := os.Stat(scaleFile); !os.IsNotExist(err) {
		t.Errorf("expected no scale file to be written, got %v", err)
	}
}

func TestRequestStandby(t *testing.T) {
	dir := t.TempDir()
	scaleFile := filepath.Join(dir, "scale_to_zero_disable")
	standbyFile := filepath.Join(dir, "standby_now")
	t.Setenv("BLAXEL_SCALE_FILE", scaleFile)
	scaleAvailableChecked, scaleAvailable = true, true
	t.Cleanup(func() { scaleAvailableChecked, scaleAvailable = false, false })

	t.Setenv(EnvStandbyNowFile, "")
	if got := GetStandbyNowFile(); got != defaultStandbyNowFile {
		t.Errorf("without %s: GetStandbyNowFile() = %q, want %q", EnvStandbyNowFile, got, defaultStandbyNowFile)
	}
	// The host serves the file: when it is absent, nothing is created in its place.
	t.Setenv(EnvStandbyNowFile, standbyFile)
	if err := RequestStandby(false); err != ErrStandbyUnsupported {
		t.Errorf("with a missing file: err = %v, want ErrStandbyUnsupported", err)
	}
	if _, err := os.Stat(standbyFile); !os.IsNotExist(err) {
		t.Errorf("missing file: standby file created (%v)", err)
	}

	if err := os.WriteFile(standbyFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for counter, want := range map[string]error{"=1": ErrHeldAwake, "garbage": ErrCounterUnknown} {
		if err := os.WriteFile(scaleFile, []byte(counter), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := RequestStandby(false); err != want {
			t.Errorf("counter %q: err = %v, want %v", counter, err, want)
		}
		if content, _ := os.ReadFile(standbyFile); len(content) != 0 {
			t.Errorf("counter %q: standby file written (%q)", counter, content)
		}
	}

	if err := os.WriteFile(scaleFile, []byte("=0"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RequestStandby(false); err != nil {
		t.Fatalf("RequestStandby(false) = %v", err)
	}
	if content, err := os.ReadFile(standbyFile); err != nil || string(content) != "+" {
		t.Errorf("standby file = %q (%v), want \"+\"", content, err)
	}
}

// force releases the holds for the standby, takes back only the ones still
// held, and still needs the host's file.
func TestRequestStandbyForce(t *testing.T) {
	dir := t.TempDir()
	scaleFile := filepath.Join(dir, "scale_to_zero_disable")
	standbyFile := filepath.Join(dir, "standby_now")
	t.Setenv("BLAXEL_SCALE_FILE", scaleFile)
	t.Setenv(EnvStandbyNowFile, standbyFile)
	scaleAvailableChecked, scaleAvailable = true, true
	oldAfter := restoreAfter
	restoreAfter = time.Hour // the test takes the holds back itself
	t.Cleanup(func() {
		scaleAvailableChecked, scaleAvailable = false, false
		restoreAfter = oldAfter
		releasedMu.Lock()
		released = 0
		releasedMu.Unlock()
	})

	if err := os.WriteFile(scaleFile, []byte("=2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RequestStandby(true); err != ErrStandbyUnsupported {
		t.Errorf("with a missing standby file: err = %v, want ErrStandbyUnsupported", err)
	}
	if released != 0 {
		t.Errorf("refused request released %d holds", released)
	}

	if err := os.WriteFile(standbyFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RequestStandby(true); err != nil {
		t.Fatalf("RequestStandby(true) while held = %v, want nil", err)
	}
	if content, _ := os.ReadFile(scaleFile); string(content) != "-2" {
		t.Errorf("scale file = %q, want the 2 holds released (\"-2\")", content)
	}
	if content, _ := os.ReadFile(standbyFile); string(content) != "+" {
		t.Errorf("standby file = %q, want \"+\"", content)
	}

	// A hold ending before the resume comes off what is taken back.
	if err := os.WriteFile(scaleFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := decrement(); err != nil {
		t.Fatal(err)
	}
	if content, _ := os.ReadFile(scaleFile); len(content) != 0 {
		t.Errorf("decrement during the release wrote %q to the counter", content)
	}

	restoreHoldsAfterResume(time.Now(), 0)
	if content, _ := os.ReadFile(scaleFile); string(content) != "+1" {
		t.Errorf("scale file after the resume = %q, want the 1 remaining hold back (\"+1\")", content)
	}
	if released != 0 {
		t.Errorf("released = %d after the restore, want 0", released)
	}
}
