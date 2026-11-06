package lifecycle

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// The reaper makes the process a subreaper, which the other tests of this
// package must not be: they run as a child process, with it on.
const reaperEnv = "LIFECYCLE_TEST_REAPER"

func TestMain(m *testing.M) {
	if os.Getenv(reaperEnv) != "" {
		ReapOrphans()
	}
	os.Exit(m.Run())
}

func TestReaper(t *testing.T) {
	if os.Getenv(reaperEnv) != "" {
		t.Skip("the driver")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^Test(Orphaned|Reaping|Adopted)", "-test.v")
	cmd.Env = append(os.Environ(), reaperEnv+"=1")
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("reaper tests: %v", err)
	}
}

func withReaper(t *testing.T) {
	t.Helper()
	if os.Getenv(reaperEnv) == "" {
		t.Skip("runs under TestReaper")
	}
}

// startWorkload starts script the way the process handler starts a workload:
// in a process group of its own, through StartChild.
func startWorkload(t *testing.T, script string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := StartChild(cmd); err != nil {
		t.Fatalf("start %q: %v", script, err)
	}
	return cmd
}

func state(pid int) string {
	fields, ok := statFields(pid)
	if !ok {
		return "gone"
	}
	return fields[0]
}

func waitForState(t *testing.T, pid int, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for state(pid) != want {
		if time.Now().After(deadline) {
			t.Fatalf("pid %d is %q, want %q", pid, state(pid), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestOrphanedGrandchildIsReapedAfterItExits(t *testing.T) {
	withReaper(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	shell := startWorkload(t, `sleep 30 & echo $! > `+pidFile)
	if err := WaitChild(shell); err != nil {
		t.Fatalf("shell: %v", err)
	}
	orphan := waitForPID(t, pidFile)
	if got := state(orphan); got == "gone" || got == "Z" {
		t.Fatalf("orphan is %q before being killed, want alive", got)
	}

	if err := syscall.Kill(orphan, syscall.SIGKILL); err != nil {
		t.Fatalf("kill: %v", err)
	}
	waitForState(t, orphan, "gone")
}

func TestReapingLeavesTheExitCodeOfAnUncollectedChild(t *testing.T) {
	withReaper(t)
	cmd := startWorkload(t, `exit 7`)
	waitForState(t, cmd.Process.Pid, "Z")
	// A zombie child and a SIGCHLD: just what an orphan looks like.
	_ = syscall.Kill(os.Getpid(), syscall.SIGCHLD)
	time.Sleep(200 * time.Millisecond)

	err := WaitChild(cmd)
	if code := cmd.ProcessState.ExitCode(); err == nil || code != 7 {
		t.Fatalf("Wait returned %v with exit code %d, want 7", err, code)
	}
}

func TestReapingLeavesTheExitCodeOfAHelperInOurGroup(t *testing.T) {
	withReaper(t)
	cmd := exec.Command("sh", "-c", "exit 3")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitForState(t, cmd.Process.Pid, "Z")
	_ = syscall.Kill(os.Getpid(), syscall.SIGCHLD)
	time.Sleep(200 * time.Millisecond)

	err := cmd.Wait()
	if code := cmd.ProcessState.ExitCode(); err == nil || code != 3 {
		t.Fatalf("Wait returned %v with exit code %d, want 3", err, code)
	}
}

func TestAdoptedPIDIsLeftForItsMonitor(t *testing.T) {
	withReaper(t)
	// Exits on request, once adopted: in LoadState the adoption comes before
	// the reaper starts.
	cmd := exec.Command("sh", "-c", `trap "exit 5" USR1; while :; do sleep 0.05; done`)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	AdoptChild(pid)
	for !Catches(pid, syscall.SIGUSR1) {
		time.Sleep(10 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGUSR1)
	waitForState(t, pid, "Z")
	_ = syscall.Kill(os.Getpid(), syscall.SIGCHLD)
	time.Sleep(200 * time.Millisecond)

	var status syscall.WaitStatus
	wpid, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	ForgetChild(pid)
	if err != nil || wpid != pid || status.ExitStatus() != 5 {
		t.Fatalf("wait4 = %d, %v, exit %d; want %d, exit 5", wpid, err, status.ExitStatus(), pid)
	}
}
