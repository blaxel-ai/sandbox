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

	"github.com/sirupsen/logrus"
)

// VictimScoreAdj is added to the spawned process' OOM score. Raising an
// oom_score_adj is always permitted, unlike lowering it, so this works even
// when the process runs unprivileged.
const VictimScoreAdj = 500

// PreferAsVictim makes the kernel OOM killer choose pid over sandbox-api.
// Best-effort: a process that already exited, or a kernel without the knob,
// is not an error for the caller.
func PreferAsVictim(pid int) {
	path := fmt.Sprintf("/proc/%d/oom_score_adj", pid)
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", VictimScoreAdj)), 0o644); err != nil {
		logrus.Debugf("[OOM] Failed to bias oom_score_adj of pid %d: %v", pid, err)
	}
}
