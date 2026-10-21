package lifecycle

import (
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
)

// sandbox-api is PID 1 of the sandbox, so every process whose parent exits is
// handed to it, and stays a zombie holding its PID until its exit status is
// collected. exec.Cmd only collects the children it started: the background
// job a stopped shell left behind was never collected. ReapOrphans collects
// them on SIGCHLD.
//
// The kernel does not tell such an orphan from a child of our own that is
// still to be collected by its Wait, and taking its status would lose the exit
// code. The distinction is the process group: a child we start in a group of
// its own - what a workload gets, so its whole tree can be signalled - goes
// through StartChild, which hides it until WaitChild has it, while the helpers
// we run ourselves (ss, fusermount, the upgrade candidate) share our group and
// are never touched.
var children = struct {
	sync.Mutex
	pids map[int]struct{}
}{pids: map[int]struct{}{}}

// StartChild starts cmd and keeps its exit status for WaitChild.
func StartChild(cmd *exec.Cmd) error {
	children.Lock()
	defer children.Unlock()
	if err := cmd.Start(); err != nil {
		return err
	}
	children.pids[cmd.Process.Pid] = struct{}{}
	return nil
}

// WaitChild waits for a child started with StartChild.
func WaitChild(cmd *exec.Cmd) error {
	err := cmd.Wait()
	ForgetChild(cmd.Process.Pid)
	return err
}

// AdoptChild keeps the exit status of a child the caller collects by PID: one
// left by the previous binary, which has no exec.Cmd for it.
func AdoptChild(pid int) {
	children.Lock()
	defer children.Unlock()
	children.pids[pid] = struct{}{}
}

// ForgetChild releases a PID once its status has been collected.
func ForgetChild(pid int) {
	children.Lock()
	defer children.Unlock()
	delete(children.pids, pid)
}

// ReapOrphans collects the exit status of orphaned processes for the life of
// this one. Outside PID 1 it adopts the orphans of its own descendants first,
// so it behaves the same under docker or a test harness.
func ReapOrphans() {
	if os.Getpid() != 1 {
		becomeSubreaper()
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGCHLD)
	go func() {
		for {
			reapOrphans()
			<-sigs
		}
	}()
}

func reapOrphans() {
	children.Lock()
	defer children.Unlock()
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return
	}
	self, group := strconv.Itoa(os.Getpid()), strconv.Itoa(syscall.Getpgrp())
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		if _, ours := children.pids[pid]; ours {
			continue
		}
		fields, ok := statFields(pid)
		if !ok || fields[0] != "Z" || fields[1] != self || fields[2] == group {
			continue
		}
		_, _ = syscall.Wait4(pid, nil, syscall.WNOHANG, nil)
	}
}
