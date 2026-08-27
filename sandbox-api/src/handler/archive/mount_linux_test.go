//go:build linux

package archive

import (
	"strings"
	"testing"
)

func TestImageDeviceIsReadFromTheMountTable(t *testing.T) {
	const rootOverlay = "24 1 0:23 / / rw,relatime - overlay overlay rw,lowerdir=/mnt/image,upperdir=/mnt/data/upper,workdir=/mnt/data/work\n"
	cases := []struct {
		name      string
		mountinfo string
		want      string
	}{
		{
			// The image is the lower layer of the root, wherever the platform
			// attached it and whatever it named it.
			name:      "the lower layer of the root overlay",
			mountinfo: "36 35 253:0 / /mnt/image ro,relatime - erofs /dev/vda ro\n" + rootOverlay,
			want:      "/dev/vda",
		},
		{
			name:      "an image attached somewhere else",
			mountinfo: "36 35 253:0 / /mnt/image ro,relatime - erofs /dev/an-unusual-device ro\n" + rootOverlay,
			want:      "/dev/an-unusual-device",
		},
		{
			// A lower layer is a directory inside the image mount as often as it
			// is the mount point itself.
			name:      "a lower layer inside the image mount",
			mountinfo: "36 35 253:0 / /mnt/image ro,relatime - erofs /dev/vda ro\n24 1 0:23 / / rw,relatime - overlay overlay rw,lowerdir=/mnt/image/rootfs,upperdir=/mnt/data/upper\n",
			want:      "/dev/vda",
		},
		{
			// An image the workload mounted is not a layer of the root, so it is
			// not the image the archive is compared against.
			name:      "an image the workload mounted",
			mountinfo: "36 35 253:0 / /mnt/image ro,relatime - erofs /dev/vda ro\n" + rootOverlay + "40 24 7:0 / /home/user/mnt ro,relatime - erofs /dev/loop0 ro\n",
			want:      "/dev/vda",
		},
		{
			name:      "no image among the layers of the root",
			mountinfo: rootOverlay + "40 24 7:0 / /home/user/mnt ro,relatime - erofs /dev/loop0 ro\n",
			want:      "",
		},
		{
			// Without an overlay to name the layers, one image mount is still an
			// answer: there is nothing else it could be.
			name:      "a root that is not an overlay",
			mountinfo: "36 35 253:0 / /mnt/image ro,relatime - erofs /dev/vda ro\n24 1 0:23 / / rw,relatime - ext4 /dev/vdb rw\n",
			want:      "/dev/vda",
		},
		{
			// Two of them and no overlay is a guess, and guessing wrong archives
			// the whole filesystem.
			name:      "two images and nothing to tell them apart",
			mountinfo: "36 35 253:0 / /mnt/image ro,relatime - erofs /dev/vda ro\n40 35 7:0 / /mnt/other ro,relatime - erofs /dev/vdc ro\n",
			want:      "",
		},
		{
			name:      "a filesystem with no device behind it",
			mountinfo: "36 35 0:42 / /mnt/image rw,relatime - tmpfs tmpfs rw\n" + rootOverlay,
			want:      "",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := imageDeviceFromMountinfo(strings.NewReader(test.mountinfo)); got != test.want {
				t.Fatalf("imageDeviceFromMountinfo = %q, want %q", got, test.want)
			}
		})
	}
}

func TestMountHoldsImageOnlyForTheImageItself(t *testing.T) {
	const mountpoint = DefaultMountPoint
	cases := []struct {
		name      string
		mountinfo string
		device    string
		want      bool
	}{
		{
			name:      "the image mounted by the export",
			mountinfo: "36 35 253:0 / " + mountpoint + " ro,relatime - erofs /dev/vda ro\n",
			want:      true,
		},
		{
			name:      "the image on the device the export was told to use",
			mountinfo: "36 35 253:0 / " + mountpoint + " ro,relatime - erofs /dev/rom0 ro\n",
			device:    "/dev/rom0",
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
			device := test.device
			if device == "" {
				device = DefaultImageDevice
			}
			if got := mountHoldsImage(strings.NewReader(test.mountinfo), mountpoint, device); got != test.want {
				t.Fatalf("mountHoldsImage = %t, want %t", got, test.want)
			}
		})
	}
}
