package blaxel

import "testing"

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
