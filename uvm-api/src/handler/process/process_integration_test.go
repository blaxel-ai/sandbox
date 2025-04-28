package process

import (
	"strings"
	"testing"
	"time"
)

// TestProcessManagerIntegration tests the complete functionality of the process manager
// This is an integration test that verifies that real processes can be started, monitored, and stopped
func TestProcessManagerIntegration(t *testing.T) {
	// Get the process manager
	pm := GetProcessManager()

	// Test starting a long-running process
	t.Run("StartLongRunningProcess", func(t *testing.T) {
		sleepPID, err := pm.StartProcess("sleep 5", "", func(process *ProcessInfo) {
			t.Logf("Process: %+v", process.stderr)
		})
		if err != nil {
			t.Fatalf("Error starting sleep process: %v", err)
		}
		t.Logf("Started sleep process with PID: %d", sleepPID)

		// Verify process exists and is running
		process, exists := pm.GetProcess(sleepPID)
		if !exists {
			t.Fatal("Sleep process should exist")
		}
		if process.Status != "running" { // Assuming "running" is the status for active processes
			t.Errorf("Expected sleep process to be running, got status: %s", process.Status)
		}

		// Test stopping the process
		err = pm.StopProcess(sleepPID)
		if err != nil {
			t.Logf("Regular stop failed (might be expected): %v", err)

			// If stopping fails, try killing it
			err = pm.KillProcess(sleepPID)
			if err != nil {
				t.Fatalf("Failed to kill sleep process: %v", err)
			}
			t.Log("Sleep process killed successfully")
		} else {
			t.Log("Sleep process stopped successfully")
		}

		// Wait for process to terminate
		time.Sleep(1 * time.Second)

		// Verify process is terminated
		process, exists = pm.GetProcess(sleepPID)
		if !exists {
			t.Fatal("Sleep process should still exist in the process list")
		}
		if process.Status != "failed" { // Assuming "terminated" is the status for stopped processes
			t.Errorf("Expected sleep process to be completed, got status: %s", process.Status)
		}
	})

	// Test process with output
	t.Run("ProcessWithOutput", func(t *testing.T) {
		expectedOutput := "Hello, Process Manager!"
		echoPID, err := pm.StartProcess("echo '"+expectedOutput+"'", "", func(process *ProcessInfo) {
			t.Logf("Process: %+v", process.stderr)
		})
		if err != nil {
			t.Fatalf("Error starting echo process: %v", err)
		}
		t.Logf("Started echo process with PID: %d", echoPID)

		// Wait for process to complete
		time.Sleep(1 * time.Second)

		// Get and verify output
		stdout, stderr, err := pm.GetProcessOutput(echoPID)
		if err != nil {
			t.Fatalf("Error getting echo process output: %v", err)
		}

		if strings.TrimSpace(stdout) != expectedOutput {
			t.Errorf("Expected stdout to be '%s', got: '%s'", expectedOutput, strings.TrimSpace(stdout))
		}

		if stderr != "" {
			t.Errorf("Expected stderr to be empty, got: '%s'", stderr)
		}

		// Verify process completed successfully
		process, exists := pm.GetProcess(echoPID)
		if !exists {
			t.Fatal("Echo process should exist")
		}
		if process.Status != "completed" {
			t.Errorf("Expected echo process to be completed, got status: %s", process.Status)
		}
		if process.ExitCode != 0 {
			t.Errorf("Expected exit code 0, got: %d", process.ExitCode)
		}
	})

	// Test process with working directory
	t.Run("ProcessWithWorkingDirectory", func(t *testing.T) {
		lsPID, err := pm.StartProcess("ls -la", "/tmp", func(process *ProcessInfo) {
			t.Logf("Process: %+v", process.stderr)
		})
		if err != nil {
			t.Fatalf("Error starting ls process: %v", err)
		}
		t.Logf("Started ls process with PID: %d in /tmp directory", lsPID)

		// Wait for process to complete
		time.Sleep(1 * time.Second)

		// Get and verify output
		stdout, stderr, err := pm.GetProcessOutput(lsPID)
		if err != nil {
			t.Fatalf("Error getting ls process output: %v", err)
		}

		// Verify that we get some output from listing /tmp
		if stdout == "" {
			t.Error("Expected stdout to contain directory listing, got empty string")
		}

		// Check if common tmp folder entries are in the output
		if !strings.Contains(stdout, "total") {
			t.Errorf("Expected ls -la output to contain 'total', output: %s", stdout)
		}

		if stderr != "" {
			t.Errorf("Expected stderr to be empty, got: '%s'", stderr)
		}

		// Verify process completed successfully
		process, exists := pm.GetProcess(lsPID)
		if !exists {
			t.Fatal("LS process should exist")
		}
		if process.Status != "completed" {
			t.Errorf("Expected ls process to be completed, got status: %s", process.Status)
		}
		if process.ExitCode != 0 {
			t.Errorf("Expected exit code 0, got: %d", process.ExitCode)
		}
	})

	// Test list processes functionality
	t.Run("ListProcesses", func(t *testing.T) {
		// Start a new process for this test
		testPID, err := pm.StartProcess("sleep 1", "", func(process *ProcessInfo) {
			t.Logf("Process: %+v", process.stderr)
		})
		if err != nil {
			t.Fatalf("Error starting test process: %v", err)
		}

		// List all processes
		processes := pm.ListProcesses()
		if len(processes) == 0 {
			t.Error("Expected at least one process, got none")
		}

		// Verify our test process is in the list
		foundTestProcess := false
		for _, proc := range processes {
			if proc.PID == testPID {
				foundTestProcess = true
				break
			}
		}
		if !foundTestProcess {
			t.Errorf("Test process PID %d not found in process list", testPID)
		}

		// Wait for process to complete
		time.Sleep(2 * time.Second)
	})
}
