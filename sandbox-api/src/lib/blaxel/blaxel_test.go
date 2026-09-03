package blaxel

import (
	"os"
	"path/filepath"
	"testing"
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
