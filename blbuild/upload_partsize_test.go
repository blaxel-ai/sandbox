package main

import "testing"

const mib = 1 << 20

func builderWith(partSizeMiB, slots int) *Builder {
	urls := make([]string, slots)
	return &Builder{Targets: &Targets{Initrd: InitrdUpload{
		PartSizeMiB: partSizeMiB, PartURLs: urls,
	}}}
}

// The regression this exists for: a 700MiB rootfs against 120 slots issued at
// 512MiB used 2 parts, so only 2 connections, on a link that caps each
// connection at ~0.2MB/s. It must now spread over every slot.
func TestUploadPartSize(t *testing.T) {
	cases := []struct {
		name      string
		sizeMiB   int64
		issuedMiB int
		slots     int
		wantParts int64
	}{
		{"typical image fans out over every slot", 700, 512, 120, 120},
		{"small image stops at the 5MiB floor", 40, 512, 120, 8},
		{"tiny image is a single part", 3, 512, 120, 1},
		{"a 60GiB image keeps the issued size", 60 * 1024, 512, 120, 120},
		{"no slots falls back to the issued size", 700, 512, 0, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			size := c.sizeMiB * mib
			got := builderWith(c.issuedMiB, c.slots).uploadPartSize(size)
			if got < s3MinPartBytes && got != int64(c.issuedMiB)*mib && size > s3MinPartBytes {
				t.Fatalf("part size %d is below S3's %d floor", got, s3MinPartBytes)
			}
			parts := (size + got - 1) / got
			if parts != c.wantParts {
				t.Errorf("got %d parts of %dMiB, want %d", parts, got/mib, c.wantParts)
			}
			if c.slots > 0 && parts > int64(c.slots) {
				t.Errorf("%d parts exceeds the %d slots granted", parts, c.slots)
			}
		})
	}
}
