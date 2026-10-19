// Package lifecycle passes the signals the host sends sandbox-api on to the
// workload it runs.
package lifecycle

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"syscall"
)

const procDir = "/proc"

// Forward sends sig to every process descended from this one or from roots
// (and to roots themselves) that has a handler for it, and returns how many were
// signalled. roots covers the processes this one adopted after a restart, which
// are no longer its children. A process without a handler is left alone, so a
// signal whose default action is to terminate (SIGUSR2) never kills a process
// that does not expect it.
func Forward(sig syscall.Signal, roots ...int) int {
	self := os.Getpid()
	targets := map[int]bool{}
	for _, pid := range descendants(append([]int{self}, roots...)...) {
		targets[pid] = true
	}
	for _, pid := range roots {
		targets[pid] = true
	}
	delete(targets, self)
	sent := 0
	for pid := range targets {
		if pid <= 0 || !Catches(pid, sig) {
			continue
		}
		if syscall.Kill(pid, sig) == nil {
			sent++
		}
	}
	return sent
}

// GroupAlive reports whether the process group still has a member that is not
// a zombie. kill(-pgid, 0) cannot tell: it also succeeds on unreaped zombies.
func GroupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		if fields, ok := statFields(pid); ok && fields[0] != "Z" && fields[2] == strconv.Itoa(pgid) {
			return true
		}
	}
	return false
}

// Catches reports whether the process has a handler installed for sig.
func Catches(pid int, sig syscall.Signal) bool {
	file, err := os.Open(procDir + "/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		mask, found := strings.CutPrefix(scanner.Text(), "SigCgt:")
		if !found {
			continue
		}
		caught, err := strconv.ParseUint(strings.TrimSpace(mask), 16, 64)
		return err == nil && caught&(1<<(uint(sig)-1)) != 0
	}
	return false
}

// descendants lists the processes below roots, children and orphans adopted by
// them alike.
func descendants(roots ...int) []int {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return nil
	}
	children := map[int][]int{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		if fields, ok := statFields(pid); ok {
			if ppid, err := strconv.Atoi(fields[1]); err == nil {
				children[ppid] = append(children[ppid], pid)
			}
		}
	}
	var found []int
	seen := map[int]bool{}
	queue := append([]int(nil), roots...)
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, child := range children[pid] {
			if seen[child] {
				continue
			}
			seen[child] = true
			found = append(found, child)
			queue = append(queue, child)
		}
	}
	return found
}

// statFields returns the fields of /proc/<pid>/stat after the command name:
// state, ppid, pgrp, and so on.
func statFields(pid int) ([]string, bool) {
	stat, err := os.ReadFile(procDir + "/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return nil, false
	}
	// The command name is in parentheses and may itself hold spaces or ')'.
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return nil, false
	}
	fields := strings.Fields(string(stat[end+1:]))
	return fields, len(fields) >= 3
}
