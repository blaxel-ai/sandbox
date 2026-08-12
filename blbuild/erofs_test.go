package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestApplyWhiteouts is the regression guard for the bug this builder exists to
// fix: a file deleted in an upper layer must be ABSENT from the produced image.
// The previous builder removed the .wh.* marker but kept the file it masked,
// which both inflated every image and shipped whatever `RUN rm secret` was meant
// to destroy.
func TestApplyWhiteouts(t *testing.T) {
	tree := t.TempDir()

	// What a lower layer put there.
	mustWrite(t, filepath.Join(tree, "secret"), "should not survive")
	mustWrite(t, filepath.Join(tree, "keep"), "should survive")
	mustMkdir(t, filepath.Join(tree, "dir"))
	mustWrite(t, filepath.Join(tree, "dir/inner"), "should not survive either")
	mustMkdir(t, filepath.Join(tree, "nested"))
	mustWrite(t, filepath.Join(tree, "nested/kept"), "should survive")

	// What the upper layer's markers say to remove.
	mustWrite(t, filepath.Join(tree, ".wh.secret"), "")
	mustWrite(t, filepath.Join(tree, "nested/.wh.gone"), "")
	mustWrite(t, filepath.Join(tree, ".wh.dir"), "")

	if err := applyWhiteouts(tree); err != nil {
		t.Fatalf("applyWhiteouts: %v", err)
	}

	assertGone(t, filepath.Join(tree, "secret"), "a whited-out file must be deleted, not just unmarked")
	assertGone(t, filepath.Join(tree, ".wh.secret"), "the marker itself must not ship")
	assertGone(t, filepath.Join(tree, "dir"), "a whited-out directory must be deleted with its contents")
	assertGone(t, filepath.Join(tree, ".wh.dir"), "the directory marker must not ship")
	assertGone(t, filepath.Join(tree, "nested/.wh.gone"), "a marker for an absent file must still be removed")

	assertExists(t, filepath.Join(tree, "keep"), "an unmarked file must survive")
	assertExists(t, filepath.Join(tree, "nested/kept"), "a sibling of a marker must survive")
}

// TestApplyWhiteoutsOpaque covers .wh..wh..opq. We remove the marker without
// wiping the directory: the entries sitting next to it were placed by the layer
// that asked for the opaque, so deleting them would drop data the layer wanted.
// This documents the deliberate limitation rather than pretending it is handled.
func TestApplyWhiteoutsOpaque(t *testing.T) {
	tree := t.TempDir()
	mustMkdir(t, filepath.Join(tree, "opaque"))
	mustWrite(t, filepath.Join(tree, "opaque/.wh..wh..opq"), "")
	mustWrite(t, filepath.Join(tree, "opaque/fresh"), "added by this layer")

	if err := applyWhiteouts(tree); err != nil {
		t.Fatalf("applyWhiteouts: %v", err)
	}

	assertGone(t, filepath.Join(tree, "opaque/.wh..wh..opq"), "the opaque marker must not ship")
	assertExists(t, filepath.Join(tree, "opaque/fresh"), "content from the layer itself must survive")
}

func TestDecompressorFor(t *testing.T) {
	cases := map[string][]string{
		"application/vnd.oci.image.layer.v1.tar+zstd":       {"zstd", "-dc"},
		"application/vnd.oci.image.layer.v1.tar+gzip":       {"gzip", "-dc"},
		"application/vnd.docker.image.rootfs.diff.tar.gzip": {"gzip", "-dc"},
		"application/vnd.oci.image.layer.v1.tar":            {"cat"},
	}
	for mediaType, want := range cases {
		got, err := decompressorFor(mediaType)
		if err != nil {
			t.Fatalf("decompressorFor(%q): %v", mediaType, err)
		}
		if got[0] != want[0] {
			t.Errorf("decompressorFor(%q) = %v, want %v", mediaType, got, want)
		}
	}

	// An unknown format must fail loudly: feeding a compressed stream to mkfs as
	// if it were a tar produces a confusing parse error much later.
	if _, err := decompressorFor("application/vnd.oci.image.layer.v1.tar+brotli"); err == nil {
		t.Error("an unknown layer format must be rejected")
	}
}

// TestErofsFlags pins the flags whose absence causes real, hard-to-trace bugs.
func TestErofsFlags(t *testing.T) {
	tarFlags := joined(erofsTar)
	for _, required := range []string{"--tar=f", "--aufs", "--ovlfs-strip=1"} {
		if !contains(erofsTar, required) {
			t.Errorf("%s is required: without --aufs and --ovlfs-strip=1, --tar=f keeps both the whiteout marker and the file it should delete (got %s)", required, tarFlags)
		}
	}
	// The value is not optional on the versions we ship: bare --incremental is
	// rejected by erofs-utils 1.8.x/1.9.x.
	if !contains(erofsIncremental, "--incremental=data") {
		t.Errorf("the incremental flag must carry its value, got %s", joined(erofsIncremental))
	}
	if !contains(erofsBase, "noinline_data") {
		t.Errorf("noinline_data is expected in the base flags, got %s", joined(erofsBase))
	}
}

func TestWorkersIsBounded(t *testing.T) {
	n := workers()
	if n < 1 || n > 8 {
		t.Errorf("workers() = %d, want between 1 and 8: a build sandbox is small and mkfs should not starve the rest", n)
	}
}

func TestLastLine(t *testing.T) {
	cases := map[string]string{
		"single":                   "single",
		"first\nlast":              "last",
		"trailing\nnewlines\n\n\n": "newlines",
		"":                         "",
	}
	for in, want := range cases {
		if got := lastLine(in); got != want {
			t.Errorf("lastLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertGone(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("%s still exists: %s", path, why)
	}
}

func assertExists(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("%s is missing: %s", path, why)
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func joined(list []string) string {
	out := ""
	for i, v := range list {
		if i > 0 {
			out += " "
		}
		out += v
	}
	return out
}
