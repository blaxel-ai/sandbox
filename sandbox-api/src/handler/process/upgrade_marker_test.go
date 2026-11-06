package process

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRequireUpgradeSourceMarker verifies upgrades only run binaries built with
// pinned upgrade sources, including when the marker straddles two reads.
func TestRequireUpgradeSourceMarker(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, content []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, content, 0755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	padding := make([]byte, 1<<20-10)

	withMarker := write("with-marker", append([]byte("\x7fELF"), upgradeSourceMarker...))
	if err := requireUpgradeSourceMarker(withMarker); err != nil {
		t.Errorf("binary with marker rejected: %v", err)
	}

	// The first read fills len(marker)-1 + 1MiB bytes; start the marker 10 bytes before that ends
	beforeBoundary := make([]byte, len(upgradeSourceMarker)-1+1<<20-10)
	split := write("split-marker", append(append(beforeBoundary, upgradeSourceMarker...), padding...))
	if err := requireUpgradeSourceMarker(split); err != nil {
		t.Errorf("marker across a read boundary rejected: %v", err)
	}

	without := write("without-marker", append(padding, "\x7fELF blaxel-sandbox-api"...))
	err := requireUpgradeSourceMarker(without)
	if err == nil || !strings.Contains(err.Error(), "predates pinned upgrade sources") {
		t.Errorf("binary without marker: got %v, want refusal", err)
	}

	if err := requireUpgradeSourceMarker(filepath.Join(dir, "missing")); err == nil {
		t.Error("missing binary accepted")
	}
}

// TestBuildCarriesUpgradeSourceMarker verifies this build would accept itself
// as an upgrade target.
func TestBuildCarriesUpgradeSourceMarker(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := requireUpgradeSourceMarker(self); err != nil {
		t.Errorf("test binary lacks the upgrade source marker: %v", err)
	}
}
