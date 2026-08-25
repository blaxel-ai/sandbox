package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// archiveMember is one entry to write into a test archive.
type archiveMember struct {
	name    string
	content string
	mode    os.FileMode
	link    string
	dir     bool
	// hardlink makes link a hardlink target instead of a symlink one.
	hardlink bool
	// typeflag overrides the member type, for the ones the export never
	// produces and the import does not restore.
	typeflag byte
}

// buildArchive writes an archive shaped like the one Export produces.
func buildArchive(t *testing.T, manifest Manifest, processes []byte, members []archiveMember) []byte {
	t.Helper()

	buffer := &bytes.Buffer{}
	tw := tar.NewWriter(buffer)

	write := func(name string, data []byte) {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg, Format: tar.FormatPAX,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}

	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	write(ManifestName, manifestJSON)
	if processes != nil {
		write(ProcessesName, processes)
	}

	for _, member := range members {
		header := &tar.Header{Name: member.name, Mode: int64(member.mode), Format: tar.FormatPAX, ModTime: time.Unix(1700000000, 0)}
		switch {
		case member.typeflag != 0:
			header.Typeflag = member.typeflag
		case member.dir:
			header.Typeflag = tar.TypeDir
			header.Name += "/"
		case member.link != "" && member.hardlink:
			header.Typeflag = tar.TypeLink
			header.Linkname = member.link
		case member.link != "":
			header.Typeflag = tar.TypeSymlink
			header.Linkname = member.link
		default:
			header.Typeflag = tar.TypeReg
			header.Size = int64(len(member.content))
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(member.content)); err != nil {
				t.Fatal(err)
			}
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// serve exposes an archive over HTTP, standing in for the presigned URL.
func serve(t *testing.T, body []byte) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server.URL + "/archive.tar"
}

func TestImportRestoresTheFilesystem(t *testing.T) {
	root := t.TempDir()
	// A file the image has and the archived sandbox deleted, and one it changed.
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"etc/deleted.conf", "etc/kept.conf", "etc/resolv.conf"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("image"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/", Deleted: []string{"etc/deleted.conf"}}, nil, []archiveMember{
		{name: "usr/bin", dir: true, mode: 0o755},
		{name: "usr/bin/tool", content: "#!/bin/sh\n", mode: 0o755},
		{name: "usr/bin/tool-link", link: "tool", mode: 0o777},
		{name: "etc/kept.conf", content: "workload", mode: 0o600},
		// Excluded by default: an archive must not hand this sandbox the DNS of
		// the one it was taken from.
		{name: "etc/resolv.conf", content: "nameserver 10.0.0.1\n", mode: 0o644},
	})

	result, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")})
	if err != nil {
		t.Fatal(err)
	}

	if result.Restored != 4 {
		t.Errorf("expected 4 restored members, got %d", result.Restored)
	}
	if result.Deleted != 1 {
		t.Errorf("expected the manifest deletion to be applied, got %d", result.Deleted)
	}
	if len(result.Skipped) != 1 || result.Skipped[0] != "etc/resolv.conf" {
		t.Errorf("expected etc/resolv.conf to be skipped, got %v", result.Skipped)
	}

	if content, err := os.ReadFile(filepath.Join(root, "etc/kept.conf")); err != nil || string(content) != "workload" {
		t.Errorf("the archived content should replace the image's, got %q (%v)", content, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "etc/deleted.conf")); !os.IsNotExist(err) {
		t.Errorf("the deleted path should be gone, got %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(root, "etc/resolv.conf")); err != nil || string(content) != "image" {
		t.Errorf("an excluded member must not be restored, got %q (%v)", content, err)
	}

	info, err := os.Lstat(filepath.Join(root, "usr/bin/tool"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("the executable bit should survive the round trip, got %v", info.Mode().Perm())
	}
	if target, err := os.Readlink(filepath.Join(root, "usr/bin/tool-link")); err != nil || target != "tool" {
		t.Errorf("expected the symlink to point at tool, got %q (%v)", target, err)
	}
}

func TestImportReadsACompressedArchive(t *testing.T) {
	root := t.TempDir()
	plain := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "srv/data", content: "payload", mode: 0o644},
	})

	compressed := &bytes.Buffer{}
	gz := gzip.NewWriter(compressed)
	if _, err := gz.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := Import(context.Background(), ImportOptions{URL: serve(t, compressed.Bytes()), root: root, MarkerPath: filepath.Join(root, "marker.json")}); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(root, "srv/data")); err != nil || string(content) != "payload" {
		t.Errorf("expected the gzipped archive to be restored, got %q (%v)", content, err)
	}
}

func TestImportRefusesAMemberOutsideTheRoot(t *testing.T) {
	root := t.TempDir()
	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "../escaped", content: "outside", mode: 0o644},
	})

	if _, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")}); err == nil {
		t.Fatal("expected an archive climbing out of the root to be refused")
	}
	if _, err := os.Lstat(filepath.Join(filepath.Dir(root), "escaped")); !os.IsNotExist(err) {
		t.Errorf("nothing should have been written outside the root, got %v", err)
	}
}

func TestImportRefusesANewerFormat(t *testing.T) {
	root := t.TempDir()
	body := buildArchive(t, Manifest{Version: ManifestVersion + 1, Root: "/"}, nil, nil)

	_, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")})
	if err == nil {
		t.Fatal("expected an archive from a newer sandbox-api to be refused")
	}
}

func TestImportFailureDoesNotLeakThePresignedURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "<Error><Code>AccessDenied</Code></Error>")
	}))
	defer server.Close()

	url := server.URL + "/archive.tar?X-Amz-Signature=deadbeef"
	_, err := Import(context.Background(), ImportOptions{URL: url, root: t.TempDir()})
	if err == nil {
		t.Fatal("expected a rejected download to fail")
	}
	if bytes.Contains([]byte(err.Error()), []byte("X-Amz-Signature")) {
		t.Errorf("the error must not carry the presigned URL: %v", err)
	}
}

func TestImportOnBootHappensOncePerFilesystem(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "marker.json")
	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/", CreatedAt: time.Unix(1700000000, 0)}, nil, []archiveMember{
		{name: "srv/data", content: "restored", mode: 0o644},
	})
	url := serve(t, body)

	options := ImportOptions{URL: url, root: root, MarkerPath: marker}
	if _, err := importOnBoot(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the import should be recorded: %v", err)
	}

	// What a restart looks like: same environment, filesystem already restored.
	// The workload's own changes must survive it.
	if err := os.WriteFile(filepath.Join(root, "srv/data"), []byte("changed by the workload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := importOnBoot(context.Background(), options); !errors.Is(err, ErrNoImport) {
		t.Errorf("a second boot must not import again, got %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(root, "srv/data")); err != nil || string(content) != "changed by the workload" {
		t.Errorf("a second boot must not overwrite what the workload wrote, got %q (%v)", content, err)
	}

	// A different archive is still refused: the filesystem carries one already.
	if _, err := importOnBoot(context.Background(), ImportOptions{URL: url + "?other=1", root: root, MarkerPath: marker}); !errors.Is(err, ErrNoImport) {
		t.Errorf("expected a restored filesystem to refuse another archive, got %v", err)
	}
}

func TestImportOnBootWithoutURL(t *testing.T) {
	if _, err := importOnBoot(context.Background(), ImportOptions{}); !errors.Is(err, ErrNoImport) {
		t.Errorf("a sandbox with nothing to import must report ErrNoImport, got %v", err)
	}
}

func TestArchiveIdentityIgnoresTheSignature(t *testing.T) {
	first := archiveIdentity("https://bucket.s3.amazonaws.com/key/delta.tar?X-Amz-Signature=aaa&X-Amz-Expires=900")
	second := archiveIdentity("https://bucket.s3.amazonaws.com/key/delta.tar?X-Amz-Signature=bbb&X-Amz-Expires=3600")
	if first != second {
		t.Errorf("two presigned URLs of the same object must identify the same archive: %q vs %q", first, second)
	}
	if first != "bucket.s3.amazonaws.com/key/delta.tar" {
		t.Errorf("unexpected identity %q", first)
	}
	if archiveIdentity("https://bucket.s3.amazonaws.com/other.tar") == first {
		t.Error("two different objects must not identify the same archive")
	}
}

func TestRelaunchSkipsProcessesThatWereNotRunning(t *testing.T) {
	state, err := json.Marshal(map[string]any{
		"version": 1,
		"processes": map[string]any{
			"proc-1": map[string]any{"pid": "proc-1", "name": "done", "command": "true", "status": "completed"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if relaunched := relaunch(state); len(relaunched) != 0 {
		t.Errorf("only the processes that were running are relaunched, got %v", relaunched)
	}
}

func TestMarkerIsNeverArchived(t *testing.T) {
	// The marker describes the sandbox that restored an archive, not the
	// workload, so the next export must leave it behind.
	rel := "var/lib/blaxel/archive-import.json"
	if !excludedPath(rel, DefaultExcludes) {
		t.Errorf("%s should be excluded from an archive", rel)
	}
}

func TestImportDeletesADirectoryTheImageStillFills(t *testing.T) {
	// The export reports a deleted subtree as its top path, and the image being
	// restored over still holds everything below it.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "opt/tool/bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "opt/tool/bin/tool"), []byte("ELF"), 0o755); err != nil {
		t.Fatal(err)
	}

	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/", Deleted: []string{"opt"}}, nil, nil)
	result, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")})
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 1 {
		t.Errorf("expected the deletion to be applied, got %d", result.Deleted)
	}
	if _, err := os.Lstat(filepath.Join(root, "opt")); !os.IsNotExist(err) {
		t.Errorf("the deleted subtree should be gone, got %v", err)
	}
}

func TestImportRestoresADirectoryOverAnImageFile(t *testing.T) {
	// The archived sandbox replaced one of the image's files with a directory,
	// which mkdir over the file would refuse with ENOTDIR.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "data"), []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}

	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "data", dir: true, mode: 0o755},
		{name: "data/file", content: "workload", mode: 0o644},
	})
	if _, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")}); err != nil {
		t.Fatal(err)
	}

	if content, err := os.ReadFile(filepath.Join(root, "data/file")); err != nil || string(content) != "workload" {
		t.Errorf("expected the archived directory to replace the image's file, got %q (%v)", content, err)
	}
}

func TestImportResolvesAHardlinkAgainstTheArchiveRoot(t *testing.T) {
	root := t.TempDir()
	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "usr/share/data", content: "shared", mode: 0o644},
		// A hardlink's target is a member name, so it is relative to the archive
		// root and not to the directory the link itself lives in.
		{name: "opt/app/data", link: "usr/share/data", mode: 0o644, hardlink: true},
	})

	if _, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, "opt/app/data"))
	if err != nil || string(content) != "shared" {
		t.Fatalf("expected the hardlink to point at the archived file, got %q (%v)", content, err)
	}
}

func TestImportRefusesAMemberThatHidesATraversal(t *testing.T) {
	root := t.TempDir()
	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		// Cleaning this name lands on an excluded path, so the exclude list has
		// to be consulted on the cleaned name and a traversal refused outright.
		{name: "etc/../etc/resolv.conf", content: "nameserver 10.0.0.1\n", mode: 0o644},
	})

	if _, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")}); err == nil {
		t.Fatal("expected the import to refuse a member climbing out of its directory")
	}
	if _, err := os.Lstat(filepath.Join(root, "etc/resolv.conf")); !os.IsNotExist(err) {
		t.Errorf("nothing should have been written, got %v", err)
	}
}

func TestImportRefusesAMemberWrittenThroughASymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("host"), 0o600); err != nil {
		t.Fatal(err)
	}

	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "link", link: outside, mode: 0o777},
		{name: "link/secret", content: "owned", mode: 0o644},
	})

	if _, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")}); err == nil {
		t.Fatal("expected the import to refuse writing through a restored symlink")
	}
	if content, err := os.ReadFile(filepath.Join(outside, "secret")); err != nil || string(content) != "host" {
		t.Errorf("the file behind the symlink must be untouched, got %q (%v)", content, err)
	}
}

func TestTransportErrorsDoNotCarryThePresignedURL(t *testing.T) {
	presigned := "https://bucket.s3.amazonaws.com/archive.tar?X-Amz-Signature=deadbeef"
	err := redactURL(&url.Error{Op: "Get", URL: presigned, Err: errors.New("dial tcp: i/o timeout")})
	if strings.Contains(err.Error(), "X-Amz-Signature") {
		t.Errorf("a transport error must not carry the signature: %v", err)
	}
	if !strings.Contains(err.Error(), "bucket.s3.amazonaws.com/archive.tar") || !strings.Contains(err.Error(), "i/o timeout") {
		t.Errorf("the object and the reason should survive the redaction, got %v", err)
	}
}

func TestImportRefusesToReplaceADirectoryHoldingAnExcludedPath(t *testing.T) {
	// etc/resolv.conf is never restored, but a symlink named etc carries the
	// whole directory off with it, this VM's resolver configuration included.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc/resolv.conf"), []byte("platform"), 0o644); err != nil {
		t.Fatal(err)
	}

	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "etc", link: "/tmp/attacker", mode: 0o777},
	})

	if _, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")}); err == nil {
		t.Fatal("expected a member shadowing a directory that holds an excluded path to be refused")
	}
	if content, err := os.ReadFile(filepath.Join(root, "etc/resolv.conf")); err != nil || string(content) != "platform" {
		t.Errorf("the platform's resolver configuration should be untouched, got %q (%v)", content, err)
	}
}

func TestImportRefusesToDeleteADirectoryHoldingAnExcludedPath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc/resolv.conf"), []byte("platform"), 0o644); err != nil {
		t.Fatal(err)
	}

	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/", Deleted: []string{"etc"}}, nil, nil)

	result, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")})
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 0 {
		t.Errorf("expected the deletion to be refused, got %d applied", result.Deleted)
	}
	if content, err := os.ReadFile(filepath.Join(root, "etc/resolv.conf")); err != nil || string(content) != "platform" {
		t.Errorf("the platform's resolver configuration should be untouched, got %q (%v)", content, err)
	}
}

func TestImportRefusesAHardlinkToAnExcludedPath(t *testing.T) {
	// A hardlink is the same file under another name: linking an excluded path
	// into the restored tree hands over exactly what excluding it withheld.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bl/token"), []byte("credential"), 0o600); err != nil {
		t.Fatal(err)
	}

	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "blaxel/stolen", link: "bl/token", hardlink: true, mode: 0o644},
	})

	if _, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")}); err == nil {
		t.Fatal("expected a hardlink to an excluded path to be refused")
	}
	if _, err := os.Lstat(filepath.Join(root, "blaxel/stolen")); !os.IsNotExist(err) {
		t.Errorf("nothing should have been linked, got %v", err)
	}
}

func TestImportDoesNotWriteThroughASymlinkAtTheTemporaryPath(t *testing.T) {
	// writeFile stages a member next to its target, at a name derived from the
	// archive: a symlink planted there must not redirect the write.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "srv"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(outside, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "srv/data.blaxel-import")); err != nil {
		t.Fatal(err)
	}

	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "srv/data", content: "payload", mode: 0o644},
	})

	if _, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")}); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(root, "srv/data")); err != nil || string(content) != "payload" {
		t.Errorf("expected the member to be restored, got %q (%v)", content, err)
	}
	if content, err := os.ReadFile(outside); err != nil || string(content) != "untouched" {
		t.Errorf("the import must not write through the planted symlink, got %q (%v)", content, err)
	}
}

func TestImportReportsThatItWrotePartOfTheArchive(t *testing.T) {
	// A truncated archive: the first member is restored, then the stream ends in
	// the middle of the second. The caller has to be able to tell this from a
	// failure that touched nothing, since the filesystem is now a mix of the
	// image's files and the archive's.
	root := t.TempDir()
	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "srv/first", content: "restored", mode: 0o644},
		{name: "srv/second", content: strings.Repeat("x", 4096), mode: 0o644},
	})

	_, err := Import(context.Background(), ImportOptions{URL: serve(t, body[:len(body)-3072]), root: root, MarkerPath: filepath.Join(root, "marker.json")})
	if !errors.Is(err, ErrPartialImport) {
		t.Fatalf("expected a partially applied archive to be reported as such, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "marker.json")); !os.IsNotExist(err) {
		t.Errorf("a failed import must not be recorded as done, got %v", err)
	}
}

func TestImportDoesNotCountSkippedMembersAsWritten(t *testing.T) {
	// A member of a type the import does not restore writes nothing, so a later
	// failure is not a partial import and the sandbox may still boot on its
	// image. Here the fifo is followed by a member the truncated stream cuts off.
	root := t.TempDir()
	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "srv/pipe", mode: 0o644, typeflag: tar.TypeFifo},
		{name: "srv/second", content: strings.Repeat("x", 4096), mode: 0o644},
	})

	result, err := Import(context.Background(), ImportOptions{URL: serve(t, body[:len(body)-4608]), root: root, MarkerPath: filepath.Join(root, "marker.json")})
	if err == nil {
		t.Fatal("expected the truncated archive to fail")
	}
	if errors.Is(err, ErrPartialImport) {
		t.Errorf("a skipped member changes nothing, so nothing was written: %v", err)
	}
	if result != nil {
		t.Errorf("expected no result for a failed import, got %+v", result)
	}
	if _, err := os.Lstat(filepath.Join(root, "srv/pipe")); !os.IsNotExist(err) {
		t.Errorf("a member of an unsupported type must not be created, got %v", err)
	}
}

func TestImportSkipsMembersOfAnUnsupportedType(t *testing.T) {
	root := t.TempDir()
	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "srv/pipe", mode: 0o644, typeflag: tar.TypeFifo},
		{name: "srv/data", content: "payload", mode: 0o644},
	})

	result, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")})
	if err != nil {
		t.Fatal(err)
	}
	if result.Restored != 1 {
		t.Errorf("expected only the regular file to be restored, got %d", result.Restored)
	}
	if len(result.Skipped) != 1 || result.Skipped[0] != "srv/pipe" {
		t.Errorf("expected the fifo to be reported as skipped, got %v", result.Skipped)
	}
}

func TestImportFailingBeforeItWritesIsNotPartial(t *testing.T) {
	root := t.TempDir()
	body := buildArchive(t, Manifest{Version: ManifestVersion + 1, Root: "/"}, nil, nil)

	_, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")})
	if err == nil {
		t.Fatal("expected the import to fail")
	}
	if errors.Is(err, ErrPartialImport) {
		t.Errorf("an import that wrote nothing must let the sandbox boot on its image: %v", err)
	}
}

func TestImportRecordsItselfBeforeRelaunchingProcesses(t *testing.T) {
	// The record has to exist before the archive has any effect: a crash between
	// the two would leave the next boot importing the archive again, on top of
	// the processes the first import already started.
	root := t.TempDir()
	processes := []byte(`[{"name":"ticker","command":"sleep 1","status":"running"}]`)
	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/", Processes: true}, processes, []archiveMember{
		{name: "srv/data", content: "restored", mode: 0o644},
	})

	recordedBeforeRelaunch := false
	options := ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")}
	options.onRestored = func(*ImportResult) error {
		recordedBeforeRelaunch = true
		return nil
	}
	if _, err := Import(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if !recordedBeforeRelaunch {
		t.Error("the import must be recorded before anything is relaunched from it")
	}
}

func TestImportThatCannotBeRecordedIsPartial(t *testing.T) {
	// The filesystem carries the archive but the sandbox cannot promise to
	// import it only once, so it must not boot on it: importing it twice would
	// undo whatever ran in between.
	root := t.TempDir()
	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "srv/data", content: "restored", mode: 0o644},
	})

	options := ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")}
	options.onRestored = func(*ImportResult) error {
		return errors.New("no space left on device")
	}
	if _, err := Import(context.Background(), options); !errors.Is(err, ErrPartialImport) {
		t.Fatalf("an import that cannot be recorded must be reported as partial, got %v", err)
	}
}
