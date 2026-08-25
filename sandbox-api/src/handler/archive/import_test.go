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
	"github.com/blaxel-ai/sandbox-api/src/handler/process"
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

func TestImportOnBootFinishesARelaunchAnEarlierBootRecordedButNeverStarted(t *testing.T) {
	// The record is written before anything is relaunched, so stopping in
	// between - a crash, an OOM kill - leaves a filesystem that carries the
	// archive and a workload that never ran. The record says so, and the next
	// boot starts the processes instead of restoring the archive again.
	root := t.TempDir()
	marker := filepath.Join(root, "marker.json")
	if err := os.MkdirAll(filepath.Join(root, "srv"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "srv/data"), []byte("changed by the workload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeMarker(marker, Marker{
		Version:          ManifestVersion,
		Archive:          "archive.tar",
		Restored:         1,
		PendingProcesses: json.RawMessage(`{"processes":{"resumed-by-the-next-boot":{"name":"resumed-by-the-next-boot","command":"sleep 0.1","status":"running"}}}`),
	}); err != nil {
		t.Fatal(err)
	}

	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "srv/data", content: "restored", mode: 0o644},
	})
	result, err := importOnBoot(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: marker})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Relaunched) != 1 {
		t.Errorf("the process the earlier boot recorded should have been started, got %v and %v as failures", result.Relaunched, result.FailedRelaunches)
	}
	if content, err := os.ReadFile(filepath.Join(root, "srv/data")); err != nil || string(content) != "changed by the workload" {
		t.Errorf("the archive must not be restored a second time, got %q (%v)", content, err)
	}

	// And the record no longer asks for the relaunch, so a later boot does not
	// start a second copy of the workload.
	recorded, err := readMarker(marker)
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded.PendingProcesses) != 0 {
		t.Error("the record must drop the processes once they are started")
	}
	if _, err := importOnBoot(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: marker}); !errors.Is(err, ErrNoImport) {
		t.Errorf("a boot after the relaunch has nothing to do, got %v", err)
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

func TestRelaunchDoesNotStartASecondCopyOfAProcessNamedLikeANumber(t *testing.T) {
	// The workload names its processes, so a name may read as a number - and a
	// number is a PID for whoever looks a process up by identifier. A resumed
	// relaunch that misses the running process starts the workload twice.
	pm := process.GetProcessManager()
	live, err := pm.StartProcessWithName("sleep 30", "", "42", nil, false, 0, false, 0, func(*process.ProcessInfo) {})
	if err != nil {
		t.Fatalf("failed to start the process the archive also carries: %v", err)
	}
	defer func() { _ = pm.KillProcess(live) }()

	state, err := json.Marshal(map[string]any{
		"version": 1,
		"processes": map[string]any{
			"proc-1": map[string]any{"pid": "proc-1", "name": "42", "command": "sleep 30", "status": "running"},
		},
	})
	if err != nil {
		t.Fatalf("failed to build the archived process list: %v", err)
	}

	relaunched, failed := relaunch(DefaultRoot, state)
	if len(relaunched) != 0 || len(failed) != 0 {
		t.Fatalf("a process that already runs must not be started again, got %v and %v as failures", relaunched, failed)
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
	// Nothing beside it either: this API opens what it keeps there as root, so a
	// member of the archive's choosing must not land in that directory.
	for _, beside := range []string{
		"var/lib/blaxel",
		"var/lib/blaxel/archive-import.json.tmp",
		"var/lib/blaxel/anything",
	} {
		if !excludedPath(beside, DefaultExcludes) {
			t.Errorf("%s should be excluded from an archive", beside)
		}
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
	var recordedPending []byte
	options := ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")}
	options.onRestored = func(_ *ImportResult, pending []byte) error {
		recordedBeforeRelaunch = true
		recordedPending = pending
		return nil
	}
	if _, err := Import(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if !recordedBeforeRelaunch {
		t.Error("the import must be recorded before anything is relaunched from it")
	}
	// And the record must say the processes are still to be started, so a crash
	// here does not leave a filesystem that looks fully imported with a workload
	// that never ran.
	if len(recordedPending) == 0 {
		t.Error("the record must carry the processes that are not started yet")
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
	options.onRestored = func(*ImportResult, []byte) error {
		return errors.New("no space left on device")
	}
	if _, err := Import(context.Background(), options); !errors.Is(err, ErrPartialImport) {
		t.Fatalf("an import that cannot be recorded must be reported as partial, got %v", err)
	}
}

func TestImportOnBootDoesNotBootOnAnUnreadableMarker(t *testing.T) {
	resetRestore(t)
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

func TestImportKilledWhileWritingIsNeverReadAsACleanImage(t *testing.T) {
	resetRestore(t)
	// An import killed while it extracts - an OOM on a large archive - records
	// nothing itself: the quarantine never runs and the freeze dies with the
	// process. What it leaves is a filesystem holding part of the archive, and
	// the next start must not take it for the image's own.
	root := t.TempDir()
	marker := filepath.Join(root, "marker.json")
	if err := writeMarker(marker, startedMarker("bucket/key")); err != nil {
		t.Fatal(err)
	}

	// The archive cannot be fetched this time, which on a clean image is a
	// sandbox that simply boots on the image. Here it is not: the filesystem
	// still holds what the killed import wrote.
	unreachable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer unreachable.Close()

	if _, err := importOnBoot(context.Background(), ImportOptions{URL: unreachable.URL, root: root, MarkerPath: marker}); !errors.Is(err, ErrPartialImport) {
		t.Fatalf("a failed retry of an import that was writing must keep the workload from starting, got %v", err)
	}
	recorded, err := readMarker(marker)
	if err != nil || recorded == nil || !recorded.Partial {
		t.Fatalf("the filesystem must stay recorded as partially restored, got %+v (%v)", recorded, err)
	}

	// Restoring the archive again is what makes that filesystem whole, so a
	// retry that succeeds boots the workload as usual.
	root = t.TempDir()
	marker = filepath.Join(root, "marker.json")
	if err := writeMarker(marker, startedMarker("bucket/key")); err != nil {
		t.Fatal(err)
	}
	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "srv/data", content: "restored", mode: 0o644},
	})
	result, err := importOnBoot(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: marker})
	if err != nil {
		t.Fatalf("expected the archive to be restored again, got %v", err)
	}
	if result.Restored != 1 {
		t.Errorf("expected the archive's member to be restored, got %d", result.Restored)
	}
	if recorded, err := readMarker(marker); err != nil || recorded == nil || recorded.Started || recorded.Partial {
		t.Fatalf("a finished import must replace the record of the attempt, got %+v (%v)", recorded, err)
	}
}

func TestImportThatWroteNothingLeavesNoAttemptBehind(t *testing.T) {
	resetRestore(t)
	// An archive that cannot be downloaded leaves the image untouched, so the
	// sandbox boots on it - and a later start must not read the attempt as one
	// that may have written.
	root := t.TempDir()
	marker := filepath.Join(root, "marker.json")
	unreachable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer unreachable.Close()

	_, err := importOnBoot(context.Background(), ImportOptions{URL: unreachable.URL, root: root, MarkerPath: marker})
	if err == nil || errors.Is(err, ErrPartialImport) {
		t.Fatalf("an archive that could not be downloaded wrote nothing, got %v", err)
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatalf("the attempt must be forgotten, got %v", err)
	}
}

func TestImportOnBootNeverRestoresOverAPartialImport(t *testing.T) {
	resetRestore(t)
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

	// The filesystem is a mix of the image and the archive, and nothing may add
	// to it - not even a terminal, which the restore's own freeze still serves.
	// The quarantine is taken by the import itself rather than left to whatever
	// called it.
	if state := Status().State; state != StateQuiesced {
		t.Errorf("a partially restored filesystem must be quarantined as the import returns, got %q", state)
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

func TestImportOnBootRefusesAnAlreadyReadOnlyRoot(t *testing.T) {
	// A partial import whose record could not be written leaves the root
	// read-only and nothing else. sandbox-api starting again adopts that mount as
	// a freeze, and importing then would restore over the files the first attempt
	// changed - and a softer failure would boot the workload on the mix.
	quiesceMu.Lock()
	previous := quiesceStatus
	quiesceStatus = QuiesceStatus{State: StateQuiesced, Reason: "an interrupted archive", ReadOnlyRoot: true}
	quiesceMu.Unlock()
	t.Cleanup(func() {
		quiesceMu.Lock()
		quiesceStatus = previous
		quiesceMu.Unlock()
	})

	root := t.TempDir()
	url := serve(t, buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "srv/file", content: "restored", mode: 0o644},
	}))
	if _, err := importOnBoot(context.Background(), ImportOptions{
		URL:        url,
		root:       root,
		MarkerPath: filepath.Join(root, "marker.json"),
	}); !errors.Is(err, ErrPartialImport) {
		t.Fatalf("expected a read-only root to be reported as a partial import, got %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "srv/file")); !os.IsNotExist(err) {
		t.Errorf("nothing may be restored over the filesystem a failed import left, got %v", err)
	}
}

func TestImportRefusesAnOversizedProcessList(t *testing.T) {
	// The manifest and the process list are the two members read into memory, and
	// an archive is data: a huge one would have the boot allocate until the API
	// is killed.
	if _, err := readMetadata(strings.NewReader(strings.Repeat("x", 16)), ProcessesName); err != nil {
		t.Fatalf("a small process list must be read, got %v", err)
	}
	oversized := strings.NewReader(strings.Repeat("x", maxMetadataBytes+1))
	if _, err := readMetadata(oversized, ProcessesName); err == nil {
		t.Fatal("a process list larger than the bound must be refused")
	}
}

func TestImportDoesNotQuarantineOnAFailureThatWroteNothing(t *testing.T) {
	// The parents of this member cannot be created - the root holds a file where
	// the archive has a directory - but they were not created either: the
	// filesystem is still the image's, and quarantining it would refuse to start
	// a workload whose files have nothing wrong with them.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "srv"), []byte("a file, not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	url := serve(t, buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "srv/nested/file", content: "restored", mode: 0o644},
	}))

	_, err := Import(context.Background(), ImportOptions{URL: url, root: root})
	if err == nil {
		t.Fatal("expected the member to fail")
	}
	if errors.Is(err, ErrPartialImport) {
		t.Fatalf("a failure that wrote nothing must not report a partial import, got %v", err)
	}
}

func TestImportKeepsAMemberNamedLikeTheStagingFile(t *testing.T) {
	// The staging name used to be derived from the target, so restoring "app"
	// removed a member the archive had already restored beside it.
	root := t.TempDir()
	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "srv/app.blaxel-import", content: "restored", mode: 0o644},
		{name: "srv/app", content: "binary", mode: 0o755},
	})

	result, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")})
	if err != nil {
		t.Fatal(err)
	}
	if result.Restored != 2 {
		t.Errorf("expected both members to be restored, got %d", result.Restored)
	}
	if content, err := os.ReadFile(filepath.Join(root, "srv/app.blaxel-import")); err != nil || string(content) != "restored" {
		t.Errorf("the member was removed to stage another one: %q, %v", content, err)
	}
	if content, err := os.ReadFile(filepath.Join(root, "srv/app")); err != nil || string(content) != "binary" {
		t.Errorf("expected the file to be restored: %q, %v", content, err)
	}
	// Nothing of the import's own is left behind.
	entries, err := os.ReadDir(filepath.Join(root, "srv"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("expected only the archived members in the directory, got %d entries", len(entries))
	}
}

func TestImportNeverRestoresTheDynamicLoaderConfiguration(t *testing.T) {
	// The archive is data the sandbox is handed, and the binaries this API execs
	// as root - blfs on the drive route - resolve their libraries through these
	// files, so a member here would choose the code they run just as replacing
	// the binary would.
	root := t.TempDir()
	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "etc/ld.so.preload", content: "/srv/hijack.so", mode: 0o644},
		{name: "etc/ld.so.conf", content: "/srv", mode: 0o644},
		{name: "etc/ld.so.conf.d/hijack.conf", content: "/srv", mode: 0o644},
		{name: "etc/ld.so.cache", content: "cache", mode: 0o644},
		{name: "srv/data", content: "payload", mode: 0o644},
	})

	result, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")})
	if err != nil {
		t.Fatal(err)
	}
	if result.Restored != 1 {
		t.Errorf("expected only the workload's file to be restored, got %d", result.Restored)
	}
	for _, name := range []string{
		"etc/ld.so.preload",
		"etc/ld.so.conf",
		"etc/ld.so.conf.d/hijack.conf",
		"etc/ld.so.cache",
	} {
		if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Errorf("%s must never be restored, got %v", name, err)
		}
		if !excludedPath(name, DefaultExcludes) {
			t.Errorf("%s should be excluded from an archive", name)
		}
	}
}

func TestImportNeverRestoresTheMuslLoader(t *testing.T) {
	// The alpine images run musl, which reads none of the glibc files above:
	// its search path is etc/ld-musl-<arch>.path, and its interpreter is the
	// libc itself. Either one chooses the code the next root exec runs.
	root := t.TempDir()
	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/"}, nil, []archiveMember{
		{name: "etc/ld-musl-x86_64.path", content: "/srv", mode: 0o644},
		{name: "etc/ld-musl-aarch64.path", content: "/srv", mode: 0o644},
		{name: "lib/ld-musl-x86_64.so.1", content: "hijacked", mode: 0o755},
		{name: "lib/libc.musl-x86_64.so.1", content: "hijacked", mode: 0o755},
		{name: "usr/lib/ld-musl-aarch64.so.1", content: "hijacked", mode: 0o755},
		{name: "usr/lib/libc.musl-aarch64.so.1", content: "hijacked", mode: 0o755},
		{name: "srv/data", content: "payload", mode: 0o644},
	})

	result, err := Import(context.Background(), ImportOptions{URL: serve(t, body), root: root, MarkerPath: filepath.Join(root, "marker.json")})
	if err != nil {
		t.Fatal(err)
	}
	if result.Restored != 1 {
		t.Errorf("expected only the workload's file to be restored, got %d", result.Restored)
	}
	for _, name := range []string{
		"etc/ld-musl-x86_64.path",
		"etc/ld-musl-aarch64.path",
		"lib/ld-musl-x86_64.so.1",
		"lib/libc.musl-x86_64.so.1",
		"usr/lib/ld-musl-aarch64.so.1",
		"usr/lib/libc.musl-aarch64.so.1",
	} {
		if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Errorf("%s must never be restored, got %v", name, err)
		}
		if !excludedPath(name, DefaultExcludes) {
			t.Errorf("%s should be excluded from an archive", name)
		}
	}
	// The workload's own libraries still travel: only the loader is the
	// platform's.
	for _, name := range []string{
		"lib/libsomething.so.1",
		"usr/lib/python3.12/os.py",
		"etc/ld-musl-x86_64.path.bak",
	} {
		if excludedPath(name, DefaultExcludes) {
			t.Errorf("%s should still be archived", name)
		}
	}
}
