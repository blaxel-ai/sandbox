package process

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/blaxel-ai/sandbox-api/src/lib/identity"
)

// currentUmask reads the umask without changing it for longer than the call.
func currentUmask() fs.FileMode {
	mask := syscall.Umask(0)
	syscall.Umask(mask)
	return fs.FileMode(mask)
}

func realPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", path, err)
	}
	return resolved
}

// The case behind the change: an exec whose working directory does not exist
// yet (Harbor's setup `mkdir -p /work`, run with cwd=/work on an image without
// /work) runs in a freshly created directory, missing parents included, like
// `docker run -w`.
func TestStartProcessCreatesMissingWorkingDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "a", "b", "c")

	p, logs := runToCompletion(t, "pwd", dir, nil)

	if p.Status != StatusCompleted || p.ExitCode != 0 {
		t.Fatalf("status=%s exit=%d stderr=%q", p.Status, p.ExitCode, logs.Stderr)
	}
	if got := strings.TrimSpace(logs.Stdout); realPath(t, got) != realPath(t, dir) {
		t.Fatalf("pwd = %q, want %q", got, dir)
	}
	want := workingDirMode &^ currentUmask()
	for _, created := range []string{"a", "a/b", "a/b/c"} {
		info, err := os.Stat(filepath.Join(base, created))
		if err != nil {
			t.Fatalf("stat %s: %v", created, err)
		}
		if !info.IsDir() || info.Mode().Perm() != want {
			t.Errorf("%s: mode %v, want a directory with %v", created, info.Mode(), want)
		}
	}
}

// The directory is created for the process, not reset: an existing one keeps
// its mode and content.
func TestExistingWorkingDirIsLeftAlone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "existing")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	p, logs := runToCompletion(t, "ls", dir, nil)

	if p.ExitCode != 0 || strings.TrimSpace(logs.Stdout) != "keep" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", p.ExitCode, logs.Stdout, logs.Stderr)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("mode changed to %v", info.Mode().Perm())
	}
}

// A path that exists but is a file is a real error, reported as such, and the
// file is not touched.
func TestWorkingDirThatIsAFileIsRejected(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := GetProcessManager().StartProcess("true", file, nil, false, 0, false, 0, false, nil)

	if err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("err = %v, want a 'is not a directory' error", err)
	}
	if data, _ := os.ReadFile(file); string(data) != "data" {
		t.Errorf("file content changed to %q", data)
	}
}

// A parent that is a file cannot be created through: an error naming the
// problem, nothing created.
func TestWorkingDirUnderAFileIsRejected(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := GetProcessManager().StartProcess("true", filepath.Join(file, "sub", "dir"), nil, false, 0, false, 0, false, nil)

	if err == nil || !errors.Is(err, syscall.ENOTDIR) {
		t.Fatalf("err = %v, want ENOTDIR", err)
	}
	if !strings.Contains(err.Error(), "cannot use working directory") {
		t.Errorf("err = %q does not say what failed", err)
	}
}

// A relative working directory keeps its meaning (relative to the API's own
// directory, which is where the child would resolve it too) and is created
// there.
func TestRelativeWorkingDirIsCreatedRelativeToTheAPI(t *testing.T) {
	base := t.TempDir()
	t.Chdir(base)

	p, logs := runToCompletion(t, "pwd", filepath.Join("rel", "dir"), nil)

	if p.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", p.ExitCode, logs.Stderr)
	}
	want := realPath(t, filepath.Join(base, "rel", "dir"))
	if got := realPath(t, strings.TrimSpace(logs.Stdout)); got != want {
		t.Fatalf("pwd = %q, want %q", got, want)
	}
}

// Parallel execs racing to create the same directory all succeed.
func TestConcurrentExecsCreateTheSameWorkingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shared", "work")
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- prepareWorkingDir(dir)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("prepareWorkingDir: %v", err)
		}
	}
}

// What follows needs root to switch to the workload identity, as the API does
// in a sandbox with BL_SANDBOX_USER set.

var nobody = &identity.Identity{Uid: 65534, Gid: 65534, Name: "nobody", Home: "/"}

func requireRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
}

// searchableTempDir is a root-owned 0755 temporary directory the workload user
// can reach: t.TempDir and its parent are 0700, which would make every check
// below fail on the way in rather than where it should.
func searchableTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{filepath.Dir(dir), dir} {
		if err := os.Chmod(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Created as the exec user, the directory belongs to that user, who can write
// in it. Docker creates it as root, which a non-root USER cannot write to.
func TestWorkingDirIsCreatedAsTheWorkloadUser(t *testing.T) {
	requireRoot(t)
	home := filepath.Join(searchableTempDir(t), "home")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(home, nobody.Uid, nobody.Gid); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "project", "work")

	if err := prepareWorkingDirAs(nobody, dir); err != nil {
		t.Fatalf("prepareWorkingDirAs: %v", err)
	}

	for _, created := range []string{filepath.Join(home, "project"), dir} {
		info, err := os.Stat(created)
		if err != nil {
			t.Fatal(err)
		}
		st := info.Sys().(*syscall.Stat_t)
		if int(st.Uid) != nobody.Uid || int(st.Gid) != nobody.Gid {
			t.Errorf("%s owned by %d:%d, want %d:%d", created, st.Uid, st.Gid, nobody.Uid, nobody.Gid)
		}
	}
}

// The exec user cannot create what `mkdir -p` would not let it create: a clear
// permission error, and nothing created on its behalf by the root API.
func TestWorkingDirTheUserMayNotCreateIsRejected(t *testing.T) {
	requireRoot(t)
	rootOwned := searchableTempDir(t) // root:root 0755
	dir := filepath.Join(rootOwned, "work")

	err := prepareWorkingDirAs(nobody, dir)

	if err == nil || !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("err = %v, want a permission error", err)
	}
	if !strings.Contains(err.Error(), "mkdir") {
		t.Errorf("err = %q does not say which operation failed", err)
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("%s was created anyway (stat: %v)", dir, statErr)
	}
}

// An existing directory the user may not enter is reported up front rather
// than as an opaque fork/exec failure.
func TestWorkingDirTheUserMayNotEnterIsRejected(t *testing.T) {
	requireRoot(t)
	locked := filepath.Join(searchableTempDir(t), "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}

	err := prepareWorkingDirAs(nobody, locked)

	if err == nil || !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("err = %v, want a permission error", err)
	}
}
