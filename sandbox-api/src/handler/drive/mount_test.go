package drive

import "testing"

func TestCrossMountCacheCoherenceFlag(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{"true", "-crossMountCacheCoherence=true"},
		{"", "-crossMountCacheCoherence=false"},
		{"false", "-crossMountCacheCoherence=false"},
		{"TRUE", "-crossMountCacheCoherence=false"},
		{"1", "-crossMountCacheCoherence=false"},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			if got := crossMountCacheCoherenceFlag(tt.value); got != tt.want {
				t.Errorf("crossMountCacheCoherenceFlag(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}
