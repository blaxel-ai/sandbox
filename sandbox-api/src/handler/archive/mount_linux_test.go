//go:build linux

package archive

import (
	"strings"
	"testing"
)

func TestMountHoldsImageOnlyForTheImageItself(t *testing.T) {
	const mountpoint = DefaultMountPoint
	cases := []struct {
		name      string
		mountinfo string
		want      bool
	}{
		{
			name:      "the image mounted by the export",
			mountinfo: "36 35 253:0 / " + mountpoint + " ro,relatime - erofs /dev/vda ro\n",
			want:      true,
		},
		{
			name:      "the image as the unikraft rom",
			mountinfo: "36 35 253:0 / " + mountpoint + " ro,relatime - erofs /dev/ukp_rom0 ro\n",
			want:      true,
		},
		{
			name:      "a drive the workload attached at the same path",
			mountinfo: "36 35 253:0 / " + mountpoint + " rw,relatime - ext4 /dev/vdb rw\n",
			want:      false,
		},
		{
			name:      "another erofs image the workload built",
			mountinfo: "36 35 7:0 / " + mountpoint + " ro,relatime - erofs /dev/loop0 ro\n",
			want:      false,
		},
		{
			name:      "a filesystem with no device behind it",
			mountinfo: "36 35 0:42 / " + mountpoint + " rw,relatime - tmpfs tmpfs rw\n",
			want:      false,
		},
		{
			name:      "nothing mounted there",
			mountinfo: "36 35 253:0 / /mnt/other ro,relatime - erofs /dev/vda ro\n",
			want:      false,
		},
		{
			// A mount stacked over the image is what is seen through the path, so
			// the last line decides.
			name: "the workload mounted over the image",
			mountinfo: "36 35 253:0 / " + mountpoint + " ro,relatime - erofs /dev/vda ro\n" +
				"37 35 0:42 / " + mountpoint + " rw,relatime - tmpfs tmpfs rw\n",
			want: false,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := mountHoldsImage(strings.NewReader(test.mountinfo), mountpoint); got != test.want {
				t.Fatalf("mountHoldsImage = %t, want %t", got, test.want)
			}
		})
	}
}
