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

func TestRequestStandby(t *testing.T) {
	dir := t.TempDir()
	scaleFile := filepath.Join(dir, "scale_to_zero_disable")
	standbyFile := filepath.Join(dir, "standby_now")
	t.Setenv("BLAXEL_SCALE_FILE", scaleFile)
	scaleAvailableChecked, scaleAvailable = true, true
	t.Cleanup(func() { scaleAvailableChecked, scaleAvailable = false, false })

	t.Setenv(EnvStandbyNowFile, "")
	if err := RequestStandby(); err != ErrStandbyUnsupported {
		t.Errorf("without %s: err = %v, want ErrStandbyUnsupported", EnvStandbyNowFile, err)
	}
	// The host serves the file: when it is absent, nothing is created in its place.
	t.Setenv(EnvStandbyNowFile, standbyFile)
	if err := RequestStandby(); err != ErrStandbyUnsupported {
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
		if err := RequestStandby(); err != want {
			t.Errorf("counter %q: err = %v, want %v", counter, err, want)
		}
		if content, _ := os.ReadFile(standbyFile); len(content) != 0 {
			t.Errorf("counter %q: standby file written (%q)", counter, content)
		}
	}

	if err := os.WriteFile(scaleFile, []byte("=0"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RequestStandby(); err != nil {
		t.Fatalf("RequestStandby() = %v", err)
	}
	if content, err := os.ReadFile(standbyFile); err != nil || string(content) != "1" {
		t.Errorf("standby file = %q (%v), want \"1\"", content, err)
	}
}
