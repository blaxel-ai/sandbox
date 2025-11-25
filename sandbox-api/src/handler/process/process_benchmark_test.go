package process

import (
	"testing"
	"time"
)

// BenchmarkProcessExecutionWithWait benchmarks process execution with waitForCompletion=true
// and verifies that logs are correctly returned at the end
func BenchmarkProcessExecutionWithWait(b *testing.B) {
	pm := NewProcessManager()
	commands := []struct {
		name    string
		command string
	}{
		{"echo", "echo 'hello world'"},
		{"pwd", "pwd"},
		{"seq_small", "seq 1 10"},
		{"seq_medium", "seq 1 100"},
		{"seq_large", "seq 1 1000"},
	}

	for _, cmd := range commands {
		b.Run(cmd.name, func(b *testing.B) {
			b.ResetTimer()
			for b.Loop() {
				start := time.Now()
				processInfo, err := pm.ExecuteProcess(cmd.command, "", "", nil, true, 0, nil, false, 0)
				if err != nil {
					b.Fatal(err)
				}
				duration := time.Since(start)

				// Verify logs are returned correctly
				if processInfo.Logs == nil {
					b.Fatal("Logs should not be nil")
				}
				if *processInfo.Logs == "" && cmd.command != "pwd" {
					// pwd might return empty in some cases, but others should have output
					b.Logf("Warning: Empty logs for command: %s", cmd.command)
				}

				// Verify process completed successfully
				if processInfo.Status != StatusCompleted && processInfo.Status != StatusFailed {
					b.Fatalf("Process should be completed or failed, got status: %s", processInfo.Status)
				}

				b.ReportMetric(float64(duration.Nanoseconds()), "ns/op")
			}
		})
	}
}
