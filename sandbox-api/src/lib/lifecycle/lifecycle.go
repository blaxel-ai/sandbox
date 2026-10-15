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

// Forward sends sig to every process descended from this one that has a handler
// for it, and returns how many were signalled. A process without a handler is
// left alone, so a signal whose default action is to terminate (SIGUSR2) never
// kills a process that does not expect it.
func Forward(sig syscall.Signal) int {
	sent := 0
	for _, pid := range descendants(os.Getpid()) {
		if !Catches(pid, sig) {
			continue
		}
		if syscall.Kill(pid, sig) == nil {
			sent++
		}
	}
	return sent
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

// descendants lists the processes below root, children and orphans adopted by
// root alike.
func descendants(root int) []int {
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
		if ppid, ok := parentOf(pid); ok {
			children[ppid] = append(children[ppid], pid)
		}
	}
	var found []int
	queue := []int{root}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, child := range children[pid] {
			found = append(found, child)
			queue = append(queue, child)
		}
	}
	return found
}

func parentOf(pid int) (int, bool) {
	stat, err := os.ReadFile(procDir + "/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	// The command name is in parentheses and may itself hold spaces or ')'.
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return 0, false
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	return ppid, err == nil
}
