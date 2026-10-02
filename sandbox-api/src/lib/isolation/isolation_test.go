package isolation

import "testing"

func TestEnabled(t *testing.T) {
	for value, want := range map[string]bool{"": false, "false": false, "0": false, "maybe": false, "true": true, "1": true, " true ": true} {
		t.Setenv(EnvEnabled, value)
		if got := Enabled(); got != want {
			t.Fatalf("Enabled() with %s=%q = %v, want %v", EnvEnabled, value, got, want)
		}
	}
}
