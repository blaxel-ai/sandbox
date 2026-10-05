package filesystem

import (
	"os"
	"testing"
)

func TestUnixPermissions(t *testing.T) {
	for _, tc := range []struct {
		mode os.FileMode
		want uint32
	}{
		{0644, 0644}, {os.ModeDir | os.ModeSticky | 0777, 01777},
		{os.ModeSetgid | 0755, 02755}, {os.ModeSetuid | 0755, 04755},
		{os.ModeSetuid | os.ModeSetgid | os.ModeSticky | 0750, 07750},
	} {
		if got := UnixPermissions(tc.mode); got != tc.want {
			t.Errorf("mode=%v got=%o want=%o", tc.mode, got, tc.want)
		}
	}
}
