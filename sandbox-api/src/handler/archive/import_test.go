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

	"github.com/blaxel-ai/sandbox-api/src/handler/drive"
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
	relaunched, failed := relaunch(DefaultRoot, state)
	if len(relaunched) != 0 || len(failed) != 0 {
		t.Errorf("only the processes that were running are relaunched, got %v and %v as failures", relaunched, failed)
	}
}

func TestRelaunchRecreatesAWorkingDirectoryTheArchiveDoesNotCarry(t *testing.T) {
	// A process working under tmp, or under any other path that is never
	// archived, finds no such directory on a restored sandbox - and the process
	// manager refuses to start a process whose working directory is missing.
	workingDir := filepath.Join(t.TempDir(), "run", "worker")
	state, err := json.Marshal(map[string]any{
		"version": 1,
		"processes": map[string]any{
			"proc-1": map[string]any{
				"pid": "proc-1", "name": "worker", "command": "true",
				"status": "running", "workingDir": workingDir,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	relaunched, failed := relaunch(DefaultRoot, state)
	if len(relaunched) != 1 || len(failed) != 0 {
		t.Fatalf("the process should have been relaunched, got %v and %v as failures", relaunched, failed)
	}
	if info, err := os.Stat(workingDir); err != nil || !info.IsDir() {
		t.Errorf("the working directory should have been recreated, got %v", err)
	}
}

func TestRelaunchRefusesAWorkingDirectoryThatBelongsToThePlatform(t *testing.T) {
	// The process list comes out of the archive like any other member, so a
	// crafted one names any working directory it likes - and recreating it would
	// put a directory of the archive's choosing inside the trees holding the
	// credentials and metadata this VM was given.
	cases := map[string]string{
		"the injected secrets":               "/run/secrets/blaxel",
		"the platform's own tree":            "/bl/anything",
		"this API's own state":               "/var/lib/blaxel/worker",
		"a directory that is not absolute":   "relative/worker",
		"a path climbing out of the sandbox": "/blaxel/../../etc/worker",
	}
	for name, workingDir := range cases {
		t.Run(name, func(t *testing.T) {
			if err := restorableWorkingDir(DefaultRoot, workingDir); err == nil {
				t.Errorf("%s should not be recreated for an archived process", workingDir)
			}
		})
	}

	state, err := json.Marshal(map[string]any{
		"version": 1,
		"processes": map[string]any{
			"proc-1": map[string]any{
				"pid": "proc-1", "name": "worker", "command": "true",
				"status": "running", "workingDir": "/run/secrets/blaxel",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	relaunched, failed := relaunch(DefaultRoot, state)
	if len(relaunched) != 0 || len(failed) != 1 {
		t.Fatalf("the process should have been reported as failed, got %v and %v as failures", relaunched, failed)
	}
	if exists("/run/secrets/blaxel") {
		t.Error("a working directory under a path that is never restored must not be created")
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

func TestImportKeepsTheModeOfADirectoryHoldingAnExcludedPath(t *testing.T) {
	// A directory member for etc is legitimate - the archive has to be able to
	// restore what else lives there - but its mode and ownership are not: 0777 on
	// etc hands the workload the resolver configuration, the hostname and the
	// hosts file the platform injected, without the archive ever naming them.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc/resolv.conf"), []byte("platform"), 0o644); err != nil {
		t.Fatal(err)
	}

	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "etc", dir: true, mode: 0o777},
		{name: "etc/motd", content: "workload", mode: 0o644},
	})

	if _, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(root, "etc"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o022 != 0 {
		t.Errorf("etc should not have become writable by the workload, got %v", info.Mode().Perm())
	}
	// The rest of the directory is still restored, which is why the member is
	// accepted at all.
	if content, err := os.ReadFile(filepath.Join(root, "etc/motd")); err != nil || string(content) != "workload" {
		t.Errorf("expected the archived file to be restored, got %q (%v)", content, err)
	}
}

func TestImportNeverReplacesTheRunningAPI(t *testing.T) {
	// The binary this API is running is the one the supervisor execs again after
	// a crash, an OOM kill or an upgrade, as root. An archive is data the sandbox
	// is handed, so restoring it would let the archive choose the code the
	// sandbox runs next.
	root := t.TempDir()
	// The test binary itself lives under /tmp, which the default excludes already
	// cover, so the path an image actually uses is named instead.
	executable := "/usr/local/bin/sandbox-api"
	previous := executablePath
	executablePath = func() (string, error) { return executable, nil }
	defer func() { executablePath = previous }()

	name := strings.TrimPrefix(executable, "/")
	live := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(live), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, []byte("the platform's build"), 0o755); err != nil {
		t.Fatal(err)
	}

	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: name, content: "the archive's build", mode: 0o755},
	})

	if _, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")}); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(live); err != nil || string(content) != "the platform's build" {
		t.Errorf("the running API should be untouched, got %q (%v)", content, err)
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
	// image. Here the fifo is followed by a member the truncated stream cuts off,
	// in a directory the image already has - so nothing at all was created.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "srv"), 0o755); err != nil {
		t.Fatal(err)
	}
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

func TestImportIsPartialWhenAMemberFailsAfterTouchingTheFilesystem(t *testing.T) {
	// A member can fail once it has already changed the filesystem: this
	// hardlink names a source the archive never carries, and by then the file
	// the image had under that name is gone. Nothing was restored, so counting
	// restored members would call this a clean failure and let the workload
	// start on a filesystem missing a file it expects.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "srv"), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(root, "srv/data")
	if err := os.WriteFile(existing, []byte("from the image"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "srv/data", link: "srv/missing", hardlink: true, mode: 0o644},
	})

	_, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")})
	if !errors.Is(err, ErrPartialImport) {
		t.Fatalf("a member that failed after removing the image's file must be reported as partial, got %v", err)
	}
	if _, err := os.Lstat(existing); !os.IsNotExist(err) {
		t.Fatalf("expected the image's file to be gone, which is what makes this partial, got %v", err)
	}
}

func TestImportIsPartialWhenOnlyTheParentsOfAFailedMemberWereCreated(t *testing.T) {
	// The member itself is refused, but the directories leading to it were
	// created before that: the filesystem already differs from the image, so the
	// workload must not start on it.
	root := t.TempDir()
	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "srv/deep/link", link: "etc/resolv.conf", hardlink: true, mode: 0o644},
	})

	_, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")})
	if !errors.Is(err, ErrPartialImport) {
		t.Fatalf("a member that created its parents before failing must be reported as partial, got %v", err)
	}
	if !exists(filepath.Join(root, "srv/deep")) {
		t.Fatal("expected the created parents to be what makes this partial")
	}
}

func TestImportIsNotPartialWhenAFailedMemberRemovedNothing(t *testing.T) {
	// The hardlink names a source the archive never carries, like the test
	// above, but nothing stands under the member's own name: the failure
	// removed nothing, so the filesystem is still the image's and the sandbox
	// may boot on it rather than being quarantined.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "srv"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "srv/data", link: "srv/missing", hardlink: true, mode: 0o644},
	})

	_, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")})
	if err == nil {
		t.Fatal("expected the import to fail")
	}
	if errors.Is(err, ErrPartialImport) {
		t.Errorf("a failure that removed nothing must let the sandbox boot on its image: %v", err)
	}
}

func TestImportDoesNotCountDeletionsOfPathsTheImageNeverHad(t *testing.T) {
	// A deletion of a path this image does not carry removes nothing, and
	// counting it would report a filesystem the import never touched as
	// changed - which is what decides whether the workload may start.
	root := t.TempDir()
	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/", Deleted: []string{"srv/never-existed"}}, nil, []archiveMember{
		{name: "srv/data", content: "restored", mode: 0o644},
	})

	result, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")})
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 0 {
		t.Errorf("expected no deletion to be counted, got %d", result.Deleted)
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

func TestImportOnBootDoesNotBootOnAnUnreadableMarker(t *testing.T) {
	// A truncated marker - a crash or a full filesystem while it was written -
	// says an import happened without saying how it ended. Restoring again
	// would write over what the first one did, and starting the workload would
	// run it on a filesystem that may be half the image's, so neither happens.
	root := t.TempDir()
	marker := filepath.Join(root, "marker.json")
	if err := os.WriteFile(marker, []byte(`{"version":1,"archi`), 0o600); err != nil {
		t.Fatal(err)
	}
	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "srv/data", content: "restored", mode: 0o644},
	})

	if _, err := importOnBoot(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: marker}); !errors.Is(err, ErrPartialImport) {
		t.Fatalf("an unreadable marker must keep the workload from starting, got %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "srv/data")); !os.IsNotExist(err) {
		t.Fatalf("nothing may be restored over a filesystem of unknown state, got %v", err)
	}
}

func TestMarkerIsInstalledAtomically(t *testing.T) {
	// The marker is what keeps an archive from being applied twice, so it is
	// never the file a partial write left behind.
	root := t.TempDir()
	marker := filepath.Join(root, "var/lib/blaxel/archive-import.json")
	if err := writeMarker(marker, Marker{Version: ManifestVersion, Archive: "bucket/key"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(marker + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("the temporary marker must not survive the write, got %v", err)
	}
	recorded, err := readMarker(marker)
	if err != nil || recorded == nil || recorded.Archive != "bucket/key" || recorded.Partial {
		t.Fatalf("expected the marker to read back as written, got %+v (%v)", recorded, err)
	}
}

func TestImportOnBootNeverRestoresOverAPartialImport(t *testing.T) {
	// The freeze a partial import triggers only lives in memory, and the
	// read-only root it remounts to may not have taken: sandbox-api starting
	// again is the case where the archive would be applied a second time, over
	// files the first attempt already changed.
	root := t.TempDir()
	marker := filepath.Join(root, "marker.json")
	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "srv/first", content: "restored", mode: 0o644},
		{name: "srv/second", content: strings.Repeat("x", 4096), mode: 0o644},
	})
	url := serve(t, body[:len(body)-3072])

	if _, err := importOnBoot(context.Background(), ImportOptions{URL: url, root: root, MarkerPath: marker}); !errors.Is(err, ErrPartialImport) {
		t.Fatalf("expected the truncated archive to be reported as partially applied, got %v", err)
	}

	recorded, err := readMarker(marker)
	if err != nil || recorded == nil {
		t.Fatalf("a partial import must be recorded, got %+v (%v)", recorded, err)
	}
	if !recorded.Partial {
		t.Error("the record of a partial import must say so")
	}

	// The next start finds the record and refuses again, without downloading
	// anything: a served archive would now succeed and hide the mixed state.
	if _, err := importOnBoot(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: marker}); !errors.Is(err, ErrPartialImport) {
		t.Fatalf("a filesystem left by a partial import must never be restored over, got %v", err)
	}
}

func TestImportFailsWhenADeletionCannotBeApplied(t *testing.T) {
	// A path the archived sandbox had deleted and that survives the import is
	// not the filesystem the archive describes, so the workload must not start
	// on it. Here the deletion is refused by the directory's permissions.
	if os.Geteuid() == 0 {
		// Root writes through the permissions this relies on.
		t.Skip("the deletion cannot be made to fail as root")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "srv")
	if err := os.MkdirAll(filepath.Join(locked, "gone"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/", Deleted: []string{"srv/gone"}}, nil, []archiveMember{
		{name: "opt/data", content: "restored", mode: 0o644},
	})

	_, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")})
	if err == nil {
		t.Fatal("expected a deletion that cannot be applied to fail the import")
	}
	if !errors.Is(err, ErrPartialImport) {
		t.Errorf("the members restored before the deletion were written, so the failure is partial: %v", err)
	}
}

func TestMarkerIsNotWrittenThroughASymlinkAtTheTemporaryPath(t *testing.T) {
	// The marker is staged under a name of its own and renamed into place. That
	// name is on the filesystem an archive was just applied to, so a symlink
	// left there must not send the write - which runs as root - to whatever it
	// points at.
	root := t.TempDir()
	marker := filepath.Join(root, "marker.json")
	outside := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(outside, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, marker+markerTemporarySuffix); err != nil {
		t.Fatal(err)
	}

	if err := writeMarker(marker, Marker{Version: ManifestVersion, Archive: "bucket/key"}); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(outside); err != nil || string(content) != "untouched" {
		t.Errorf("the marker must not be written through the planted symlink, got %q (%v)", content, err)
	}
	recorded, err := readMarker(marker)
	if err != nil || recorded == nil || recorded.Archive != "bucket/key" {
		t.Fatalf("expected the marker to be installed anyway, got %+v (%v)", recorded, err)
	}
}

func TestImportNeverRestoresTheMarkerTemporaryPath(t *testing.T) {
	// An archive member named like the file the marker is staged under would be
	// what the next marker write opens: excluded, so the archive cannot decide
	// that at all.
	root := t.TempDir()
	name := strings.TrimPrefix(DefaultImportMarker+markerTemporarySuffix, "/")
	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: name, content: "planted", mode: 0o644},
	})

	result, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")})
	if err != nil {
		t.Fatal(err)
	}
	if result.Restored != 0 {
		t.Errorf("expected the member to be skipped, got %d restored", result.Restored)
	}
	if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
		t.Errorf("nothing should have been written at the marker's temporary path, got %v", err)
	}
}

func TestImportNeverReplacesTheDriveMountBinary(t *testing.T) {
	// blfs is executed as root by a route this API keeps serving, so it belongs
	// to the image the same way this API's own binary does: an archive that
	// could replace it would choose the code the next mount runs.
	root := t.TempDir()
	name := strings.TrimPrefix(drive.BlfsPath, "/")
	live := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(live), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, []byte("the platform's build"), 0o755); err != nil {
		t.Fatal(err)
	}

	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: name, content: "the archive's build", mode: 0o755},
	})

	if _, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")}); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(live); err != nil || string(content) != "the platform's build" {
		t.Errorf("the drive mount binary should be untouched, got %q (%v)", content, err)
	}
}

func TestImportNeverReplacesAnAPIBinaryThisProcessIsNotRunning(t *testing.T) {
	// A hot upgrade execs into sandbox-api-upgraded, so os.Executable stops
	// naming the image's own binary - the one the supervisor execs on the next
	// boot. Both names are excluded in every directory an API binary lives in,
	// whatever is running and even when nothing can be read about it.
	for name, executable := range map[string]func() (string, error){
		"after an upgrade": func() (string, error) { return "/usr/local/bin/sandbox-api-upgraded", nil },
		"unknown":          func() (string, error) { return "", errors.New("no executable") },
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			previous := executablePath
			executablePath = executable
			defer func() { executablePath = previous }()

			members := []archiveMember{}
			for _, path := range []string{"usr/local/bin/sandbox-api", "usr/local/bin/sandbox-api-upgraded", "blaxel/sandbox-api"} {
				live := filepath.Join(root, path)
				if err := os.MkdirAll(filepath.Dir(live), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(live, []byte("the platform's build"), 0o755); err != nil {
					t.Fatal(err)
				}
				members = append(members, archiveMember{name: path, content: "the archive's build", mode: 0o755})
			}

			body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, members)
			if _, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")}); err != nil {
				t.Fatal(err)
			}
			for _, member := range members {
				content, err := os.ReadFile(filepath.Join(root, member.name))
				if err != nil || string(content) != "the platform's build" {
					t.Errorf("%s should be untouched, got %q (%v)", member.name, content, err)
				}
			}
		})
	}
}

func TestImportRefusesAProcessNameThatIsAPath(t *testing.T) {
	// The process manager builds a relaunched process's log files out of its
	// name and opens them as root, so a name carrying a path of its own would
	// have the archive choose what those writes truncate.
	for _, name := range []string{
		"",
		".",
		"..",
		"../../bl/credentials",
		"sub/worker",
		"/etc/hostname",
	} {
		if err := restorableProcessName(name); err == nil {
			t.Fatalf("process name %q was accepted", name)
		}
	}
	for _, name := range []string{"worker", "my-worker.1", "worker_2"} {
		if err := restorableProcessName(name); err != nil {
			t.Fatalf("process name %q was refused: %v", name, err)
		}
	}
}
