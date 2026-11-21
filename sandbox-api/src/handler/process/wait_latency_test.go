package process

import (
	"sort"
	"testing"
	"time"
)

// A short command's waitForCompletion response must not wait out the log
// tailer's poll interval: it used to add a fixed ~50ms to every exec.
func TestShortCommandWaitSkipsTailerPollDelay(t *testing.T) {
	ProcessLogDir = t.TempDir()
	pm := NewProcessManager()
	var d []time.Duration
	for i := 0; i < 20; i++ {
		start := time.Now()
		p, err := pm.ExecuteProcess(`echo "Hello World"`, "", "", nil, true, 0, nil, false, 0, false, false)
		if err != nil {
			t.Fatal(err)
		}
		d = append(d, time.Since(start))
		output, err := pm.GetProcessOutput(p.PID)
		if err != nil || p.Status != StatusCompleted || output.Stdout != "Hello World\n" {
			t.Fatalf("status %s stdout %q err %v", p.Status, output.Stdout, err)
		}
	}
	sort.Slice(d, func(a, b int) bool { return d[a] < d[b] })
	t.Logf("min %v median %v max %v", d[0], d[len(d)/2], d[len(d)-1])
	if d[len(d)/2] >= 40*time.Millisecond {
		t.Fatalf("median echo exec %v, want under 40ms", d[len(d)/2])
	}
}
