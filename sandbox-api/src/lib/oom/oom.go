// Package oom biases the kernel's OOM killer against sandbox-api.
//
// A sandbox has no swap, so a workload that allocates too much makes the kernel
// pick a victim by oom_score, which is driven by memory usage: a python script
// holding a large model is a bigger target than sandbox-api, but not always, and
// killing sandbox-api takes the whole sandbox down (the guest supervisor
// restarts it, losing every running process). Nudging the processes we spawn
// makes them the victims instead, so the sandbox survives its own workload.
package oom

import (
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
)

// VictimScoreAdj is added to the spawned process' OOM score. Raising an
// oom_score_adj is always permitted, unlike lowering it, so this works even
// when the process runs unprivileged.
const VictimScoreAdj = 500

// SurvivorScoreAdj is sandbox-api's own bias. A child inherits it and is then
// set to VictimScoreAdj, so the 1000-point gap between the API and anything it
// spawned is what the kernel compares.
const SurvivorScoreAdj = -500

// ProtectSelf takes sandbox-api out of the OOM killer's likely picks: it holds
// every process' state and its death restarts the whole sandbox, so it must be
// the last thing the kernel reclaims. Lowering an oom_score_adj needs
// CAP_SYS_RESOURCE, so this only takes effect while we are root - best-effort
// otherwise, as the bias on the spawned processes covers the same ground.
func ProtectSelf() {
	if err := os.WriteFile("/proc/self/oom_score_adj",
		[]byte(fmt.Sprintf("%d\n", SurvivorScoreAdj)), 0o644); err != nil {
		logrus.Debugf("[OOM] Failed to protect sandbox-api from the OOM killer: %v", err)
	}
}

// HeapPercent is the share of the guest's memory sandbox-api's heap is allowed
// to grow to before the garbage collector has to work for it.
const HeapPercent = 20

// HeapFloorBytes is the smallest soft limit worth setting: below this the
// collector would run constantly for no gain.
const HeapFloorBytes = 64 * 1024 * 1024

// LimitHeap gives the Go runtime a soft memory limit, so garbage is collected
// under memory pressure rather than the heap being allowed to double while the
// guest has nothing left to give.
//
// Without it the collector's only target is a ratio of the live heap, so a burst
// of allocation - serving a process' output, say - is answered by taking more
// memory from a guest that has none, and the kernel resolves that by killing
// something. The limit is soft: exceeding it means more collection, never an
// allocation failure. Override with GOMEMLIMIT, which the runtime reads itself.
func LimitHeap() {
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}

	total := memTotalBytes()
	if total <= 0 {
		return
	}

	percent := int64(HeapPercent)
	if raw := os.Getenv("SANDBOX_MAX_HEAP_PERCENT"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 && n <= 100 {
			percent = n
		}
	}

	limit := total * percent / 100
	if limit < HeapFloorBytes {
		limit = HeapFloorBytes
	}
	debug.SetMemoryLimit(limit)
	logrus.Debugf("[OOM] Soft memory limit set to %d bytes (%d%% of %d)", limit, percent, total)
}

// memTotalBytes is the guest's total memory, or 0 when it cannot be read.
func memTotalBytes() int64 {
	content, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(content), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}

// PreferAsVictim makes the kernel OOM killer choose pid over sandbox-api.
// Best-effort: a process that already exited, or a kernel without the knob,
// is not an error for the caller.
func PreferAsVictim(pid int) {
	path := fmt.Sprintf("/proc/%d/oom_score_adj", pid)
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", VictimScoreAdj)), 0o644); err != nil {
		logrus.Debugf("[OOM] Failed to bias oom_score_adj of pid %d: %v", pid, err)
	}
}
