//go:build linux

package archive

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// A startup shell that forks instead of execing dies on SIGTERM; its child has
// to get SIGTERM too, and the stop has to wait for it.
func TestTerminateWorkloadStopsChildrenOfTheStartupCommand(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "term")
	startup := exec.Command("sh", "-c", `sh -c 'trap "sleep 0.3; touch `+marker+`; exit 0" TERM; while :; do sleep 0.05; done' & wait`)
	startup.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := startup.Start(); err != nil {
		t.Fatalf("failed to start the startup command: %v", err)
	}
	pid := startup.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	RegisterStartupWorkload(pid, nil)
	t.Cleanup(func() { RegisterStartupWorkload(0, nil) })
	go func() {
		_ = startup.Wait()
		UnregisterStartupWorkload(pid)
	}()
	// Let the child install its trap.
	time.Sleep(200 * time.Millisecond)

	TerminateWorkload()
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("TerminateWorkload returned before the startup command's child got SIGTERM and exited")
	}
}

// The startup command can leave its own process group. A group-only signal
// then misses it (ESRCH), and it must not be taken for already gone.
func TestTerminateWorkloadStopsAStartupCommandThatLeftItsGroup(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "term")
	// Joins this test's group, which is in the same session, so -pid no longer exists.
	script := `$SIG{TERM} = sub { open my $f, ">", $ARGV[1]; close $f; exit 0 }; setpgrp(0, $ARGV[0]) or die "setpgrp: $!"; sleep 1 while 1`
	startup := exec.Command("perl", "-e", script, strconv.Itoa(syscall.Getpgrp()), marker)
	startup.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := startup.Start(); err != nil {
		t.Fatalf("failed to start the startup command: %v", err)
	}
	pid := startup.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	RegisterStartupWorkload(pid, nil)
	t.Cleanup(func() { RegisterStartupWorkload(0, nil) })
	go func() {
		_ = startup.Wait()
		UnregisterStartupWorkload(pid)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if pgid, err := syscall.Getpgid(pid); err == nil && pgid != pid {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the startup command never left its process group")
		}
		time.Sleep(20 * time.Millisecond)
	}

	TerminateWorkload()
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("TerminateWorkload returned without signalling a startup command that left its group")
	}
}
