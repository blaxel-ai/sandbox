package handler

import "testing"

// TestResolveUpgradeVersion verifies the default upgrade version logic.
// Regression test for ENG-2974: an empty version must resolve to "latest"
// (the most recent published release) rather than "develop", which could
// silently downgrade the running binary.
func TestResolveUpgradeVersion(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		want      string
	}{
		{name: "empty defaults to latest", requested: "", want: "latest"},
		{name: "explicit develop is preserved", requested: "develop", want: "develop"},
		{name: "explicit main is preserved", requested: "main", want: "main"},
		{name: "explicit tag is preserved", requested: "v1.0.0", want: "v1.0.0"},
		{name: "explicit latest is preserved", requested: "latest", want: "latest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveUpgradeVersion(tt.requested); got != tt.want {
				t.Errorf("resolveUpgradeVersion(%q) = %q, want %q", tt.requested, got, tt.want)
			}
		})
	}
}
