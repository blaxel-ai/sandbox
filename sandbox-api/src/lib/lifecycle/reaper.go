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
var children struct {
	// Held shared by StartChild around the start and registration of a child,
	// so starts run in parallel, and exclusively by the reaper while it
	// collects: a PID it saw as a zombie is then either registered, or an
	// orphan.
	start sync.RWMutex
	mu    sync.Mutex
	pids  map[int]struct{}
}

func register(pid int) {
	children.mu.Lock()
	defer children.mu.Unlock()
	if children.pids == nil {
		children.pids = map[int]struct{}{}
	}
	children.pids[pid] = struct{}{}
}

func registered(pid int) bool {
	children.mu.Lock()
	defer children.mu.Unlock()
	_, ok := children.pids[pid]
	return ok
}

// StartChild starts cmd and keeps its exit status for WaitChild.
func StartChild(cmd *exec.Cmd) error {
	children.start.RLock()
	defer children.start.RUnlock()
	if err := cmd.Start(); err != nil {
		return err
	}
	register(cmd.Process.Pid)
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
	register(pid)
}

// ForgetChild releases a PID once its status has been collected.
func ForgetChild(pid int) {
	children.mu.Lock()
	defer children.mu.Unlock()
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
	// Scan without the lock: a zombie keeps its PID until collected, so the
	// candidates are still the same processes when the lock is taken, and an
	// empty scan - the usual case - costs starters nothing.
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return
	}
	var zombies []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || registered(pid) {
			continue
		}
		if isOrphanZombie(pid) {
			zombies = append(zombies, pid)
		}
	}
	if len(zombies) == 0 {
		return
	}
	children.start.Lock()
	defer children.start.Unlock()
	for _, pid := range zombies {
		// Re-check: a tracked child collected since the scan may have freed
		// the PID to a helper of ours.
		if registered(pid) || !isOrphanZombie(pid) {
			continue
		}
		_, _ = syscall.Wait4(pid, nil, syscall.WNOHANG, nil)
	}
}

// isOrphanZombie reports whether pid is a zombie child of ours outside our
// own process group.
func isOrphanZombie(pid int) bool {
	fields, ok := statFields(pid)
	return ok && fields[0] == "Z" && fields[1] == strconv.Itoa(os.Getpid()) && fields[2] != strconv.Itoa(syscall.Getpgrp())
}
