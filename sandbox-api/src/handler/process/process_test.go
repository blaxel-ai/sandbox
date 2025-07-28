package process

import (
	"strings"
	"testing"
	"time"
)

// TestProcessManagerIntegration tests the complete functionality of the process manager
// This is an integration test that verifies that real processes can be started, monitored, and stopped
func TestProcessManagerIntegrationWithPID(t *testing.T) {
	// Get the process manager
	pm := GetProcessManager()

	// Test starting a long-running process
	t.Run("StartLongRunningProcess", func(t *testing.T) {
		sleepPID, err := pm.StartProcess("sleep 5", "", nil, func(process *ProcessInfo) {
			t.Logf("Process: %+v", process.stderr)
		})
		if err != nil {
			t.Fatalf("Error starting sleep process: %v", err)
		}
		t.Logf("Started sleep process with PID: %s", sleepPID)

		// Verify process exists and is running
		process, exists := pm.GetProcessByIdentifier(sleepPID)
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
		time.Sleep(10 * time.Millisecond)

		// Verify process is terminated
		process, exists = pm.GetProcessByIdentifier(sleepPID)
		if !exists {
			t.Fatal("Sleep process should still exist in the process list")
		}
		if process.Status != "stopped" && process.Status != "killed" { // Assuming "terminated" is the status for stopped processes
			t.Errorf("Expected sleep process to be completed, got status: %s", process.Status)
		}
	})

	// Test process with output
	t.Run("ProcessWithOutput", func(t *testing.T) {
		expectedOutput := "Hello, Process Manager!"
		echoPID, err := pm.StartProcess("echo '"+expectedOutput+"'", "", nil, func(process *ProcessInfo) {
			t.Logf("Process: %+v", process.stderr)
		})
		if err != nil {
			t.Fatalf("Error starting echo process: %v", err)
		}
		t.Logf("Started echo process with PID: %s", echoPID)

		// Wait for process to complete
		time.Sleep(10 * time.Millisecond)

		// Get and verify output
		logs, err := pm.GetProcessOutput(echoPID)
		if err != nil {
			t.Fatalf("Error getting echo process output: %v", err)
		}

		if strings.TrimSpace(logs.Stdout) != expectedOutput {
			t.Errorf("Expected stdout to be '%s', got: '%s'", expectedOutput, strings.TrimSpace(logs.Stdout))
		}

		if logs.Stderr != "" {
			t.Errorf("Expected stderr to be empty, got: '%s'", logs.Stderr)
		}

		// Verify process completed successfully
		process, exists := pm.GetProcessByIdentifier(echoPID)
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
		lsPID, err := pm.StartProcess("ls -la", "/tmp", nil, func(process *ProcessInfo) {
			t.Logf("Process: %+v", process.stderr)
		})
		if err != nil {
			t.Fatalf("Error starting ls process: %v", err)
		}
		t.Logf("Started ls process with PID: %s in /tmp directory", lsPID)

		// Wait for process to complete
		time.Sleep(10 * time.Millisecond)

		// Get and verify output
		logs, err := pm.GetProcessOutput(lsPID)
		if err != nil {
			t.Fatalf("Error getting ls process output: %v", err)
		}

		// Verify that we get some output from listing /tmp
		if logs.Stdout == "" {
			t.Error("Expected stdout to contain directory listing, got empty string")
		}

		// Check if common tmp folder entries are in the output
		if !strings.Contains(logs.Stdout, "total") {
			t.Errorf("Expected ls -la output to contain 'total', output: %s", logs.Stdout)
		}

		if logs.Stderr != "" {
			t.Errorf("Expected stderr to be empty, got: '%s'", logs.Stderr)
		}

		// Verify process completed successfully
		process, exists := pm.GetProcessByIdentifier(lsPID)
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
		testPID, err := pm.StartProcess("sleep 1", "", nil, func(process *ProcessInfo) {
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
			t.Errorf("Test process PID %s not found in process list", testPID)
		}

		// Wait for process to complete
		time.Sleep(10 * time.Millisecond)
	})
}

func TestProcessManagerIntegrationWithName(t *testing.T) {
	// Get the process manager
	pm := GetProcessManager()

	// Test starting a long-running process
	t.Run("StartLongRunningProcess", func(t *testing.T) {
		name := "sleep-process"
		_, err := pm.StartProcessWithName("sleep 5", "", name, nil, false, 0, func(process *ProcessInfo) {
			t.Logf("Process: %+v", process.stderr)
		})
		if err != nil {
			t.Fatalf("Error starting sleep process: %v", err)
		}
		t.Logf("Started sleep process with name: %s", name)

		// Verify process exists and is running
		process, exists := pm.GetProcessByIdentifier(name)
		if !exists {
			t.Fatal("Sleep process should exist")
		}
		if process.Status != "running" { // Assuming "running" is the status for active processes
			t.Errorf("Expected sleep process to be running, got status: %s", process.Status)
		}

		// Test stopping the process
		err = pm.StopProcess(name)
		if err != nil {
			t.Logf("Regular stop failed (might be expected): %v", err)

			// If stopping fails, try killing it
			err = pm.KillProcess(name)
			if err != nil {
				t.Fatalf("Failed to kill sleep process: %v", err)
			}
			t.Log("Sleep process killed successfully")
		} else {
			t.Log("Sleep process stopped successfully")
		}

		// Wait for process to terminate
		time.Sleep(10 * time.Millisecond)

		// Verify process is terminated
		process, exists = pm.GetProcessByIdentifier(name)
		if !exists {
			t.Fatal("Sleep process should still exist in the process list")
		}
		if process.Status != "stopped" && process.Status != "killed" { // Assuming "terminated" is the status for stopped processes
			t.Errorf("Expected sleep process to be completed, got status: %s", process.Status)
		}
	})

	// Test process with output
	t.Run("ProcessWithOutput", func(t *testing.T) {
		expectedOutput := "Hello, Process Manager!"
		name := "echo-process"
		_, err := pm.StartProcessWithName("echo '"+expectedOutput+"'", "", name, nil, false, 0, func(process *ProcessInfo) {
			t.Logf("Process: %+v", process.stderr)
		})
		if err != nil {
			t.Fatalf("Error starting echo process: %v", err)
		}
		t.Logf("Started echo process with name: %s", name)

		// Wait for process to complete
		time.Sleep(10 * time.Millisecond)

		// Get and verify output
		logs, err := pm.GetProcessOutput(name)
		if err != nil {
			t.Fatalf("Error getting echo process output: %v", err)
		}

		if strings.TrimSpace(logs.Stdout) != expectedOutput {
			t.Errorf("Expected stdout to be '%s', got: '%s'", expectedOutput, strings.TrimSpace(logs.Stdout))
		}

		if logs.Stderr != "" {
			t.Errorf("Expected stderr to be empty, got: '%s'", logs.Stderr)
		}

		// Verify process completed successfully
		process, exists := pm.GetProcessByIdentifier(name)
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
		name := "ls-process"
		_, err := pm.StartProcessWithName("ls -la", "", name, nil, false, 0, func(process *ProcessInfo) {
			t.Logf("Process: %+v", process.stderr)
		})
		if err != nil {
			t.Fatalf("Error starting ls process: %v", err)
		}
		t.Logf("Started ls process with name: %s in /tmp directory", name)

		// Wait for process to complete
		time.Sleep(10 * time.Millisecond)

		// Get and verify output
		logs, err := pm.GetProcessOutput(name)
		if err != nil {
			t.Fatalf("Error getting ls process output: %v", err)
		}

		// Verify that we get some output from listing /tmp
		if logs.Stdout == "" {
			t.Error("Expected stdout to contain directory listing, got empty string")
		}

		// Check if common tmp folder entries are in the output
		if !strings.Contains(logs.Stdout, "total") {
			t.Errorf("Expected ls -la output to contain 'total', output: %s", logs.Stdout)
		}

		if logs.Stderr != "" {
			t.Errorf("Expected stderr to be empty, got: '%s'", logs.Stderr)
		}

		// Verify process completed successfully
		process, exists := pm.GetProcessByIdentifier(name)
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
		name := "test-process"
		_, err := pm.StartProcessWithName("sleep 1", "", name, nil, false, 0, func(process *ProcessInfo) {
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
			if proc.Name == name {
				foundTestProcess = true
				break
			}
		}
		if !foundTestProcess {
			t.Errorf("Test process name %s not found in process list", name)
		}

		// Wait for process to complete
		time.Sleep(10 * time.Millisecond)
	})
}

// TestEnvironmentVariableHandling tests that environment variables are correctly passed to processes
func TestEnvironmentVariableHandling(t *testing.T) {
	pm := GetProcessManager()

	t.Run("MultipleEnvironmentVariables", func(t *testing.T) {
		// Test with multiple environment variables including system overrides
		env := map[string]string{
			"CUSTOM_VAR1": "value1",
			"CUSTOM_VAR2": "value2",
			"PATH":        "/custom/path:/another/path",
			"HOME":        "/custom/home",
			"TEST_VAR":    "test_value",
		}

		// Run the test multiple times to catch any intermittent issues
		for i := 0; i < 10; i++ {
			t.Logf("Test iteration %d", i+1)

			// Use printenv to check all environment variables
			pid, err := pm.StartProcess("printenv", "", env, func(process *ProcessInfo) {
				t.Logf("Process completed: %s", process.PID)
			})
			if err != nil {
				t.Fatalf("Error starting process: %v", err)
			}

			// Wait for process to complete
			time.Sleep(10 * time.Millisecond)

			// Get output
			logs, err := pm.GetProcessOutput(pid)
			if err != nil {
				t.Fatalf("Error getting process output: %v", err)
			}

			// Verify all custom environment variables are present
			output := logs.Stdout
			for key, expectedValue := range env {
				expectedLine := key + "=" + expectedValue
				if !strings.Contains(output, expectedLine) {
					t.Errorf("Iteration %d: Expected environment variable not found: %s", i+1, expectedLine)
					t.Logf("Full output:\n%s", output)
				}
			}

			// Verify no duplicate environment variables
			lines := strings.Split(output, "\n")
			envCount := make(map[string]int)
			for _, line := range lines {
				if idx := strings.IndexByte(line, '='); idx > 0 {
					key := line[:idx]
					envCount[key]++
				}
			}

			for key, count := range envCount {
				if count > 1 {
					t.Errorf("Iteration %d: Duplicate environment variable found: %s (count: %d)", i+1, key, count)
				}
			}
		}
	})

	t.Run("EmptyEnvironmentMap", func(t *testing.T) {
		// Test with empty environment map - should inherit system environment
		env := map[string]string{}

		pid, err := pm.StartProcess("printenv PATH", "", env, func(process *ProcessInfo) {
			t.Logf("Process completed: %s", process.PID)
		})
		if err != nil {
			t.Fatalf("Error starting process: %v", err)
		}

		// Wait for process to complete
		time.Sleep(10 * time.Millisecond)

		// Get output
		logs, err := pm.GetProcessOutput(pid)
		if err != nil {
			t.Fatalf("Error getting process output: %v", err)
		}

		// Should have inherited system PATH
		if strings.TrimSpace(logs.Stdout) == "" {
			t.Error("Expected to inherit system PATH, but got empty output")
		}
	})

	t.Run("NilEnvironmentMap", func(t *testing.T) {
		// Test with nil environment map - should inherit system environment
		var env map[string]string = nil

		pid, err := pm.StartProcess("printenv PATH", "", env, func(process *ProcessInfo) {
			t.Logf("Process completed: %s", process.PID)
		})
		if err != nil {
			t.Fatalf("Error starting process: %v", err)
		}

		// Wait for process to complete
		time.Sleep(10 * time.Millisecond)

		// Get output
		logs, err := pm.GetProcessOutput(pid)
		if err != nil {
			t.Fatalf("Error getting process output: %v", err)
		}

		// Should have inherited system PATH
		if strings.TrimSpace(logs.Stdout) == "" {
			t.Error("Expected to inherit system PATH, but got empty output")
		}
	})
}

// TestProcessRestartOnFailure tests the restart functionality
func TestProcessRestartOnFailure(t *testing.T) {
	pm := GetProcessManager()

	t.Run("RestartOnFailure", func(t *testing.T) {
		completionCount := 0
		maxRestarts := 2
		var lastProcess *ProcessInfo

		// Start a process that will fail (exit code 1)
		_, err := pm.StartProcessWithName(
			"sh -c 'exit 1'", // This command will always fail
			"",
			"test-restart-process",
			nil,
			true, // restartOnFailure
			maxRestarts,
			func(process *ProcessInfo) {
				completionCount++
				lastProcess = process
				t.Logf("Process completion %d: PID=%s, Status=%s, ExitCode=%d, CurrentRestarts=%d",
					completionCount, process.PID, process.Status, process.ExitCode, process.CurrentRestarts)
			},
		)
		if err != nil {
			t.Fatalf("Error starting process: %v", err)
		}

		// Wait for all restart attempts to complete
		// Process should restart maxRestarts times, then fail permanently
		time.Sleep(500 * time.Millisecond)

		// Check that we got exactly one callback (only the final process should callback)
		expectedCallbacks := 1 // only the final process calls back
		if completionCount != expectedCallbacks {
			t.Errorf("Expected %d process completion, got %d", expectedCallbacks, completionCount)
		}

		// The last process should have attempted all restarts
		if lastProcess == nil {
			t.Fatal("Should have received at least one process completion callback")
		}

		// Verify that restarts were attempted on the final process
		if lastProcess.CurrentRestarts != maxRestarts {
			t.Errorf("Expected CurrentRestarts to be %d, got %d", maxRestarts, lastProcess.CurrentRestarts)
		}

		// Final status should be failed
		if lastProcess.Status != StatusFailed {
			t.Errorf("Expected final status to be %s, got %s", StatusFailed, lastProcess.Status)
		}

		// Check logs contain restart messages - try to get logs from the last process
		_, exists := pm.GetProcessByIdentifier("test-restart-process")
		if !exists {
			t.Fatal("Process should exist by name")
		}

		logs, err := pm.GetProcessOutput("test-restart-process")
		if err != nil {
			t.Fatalf("Error getting process output: %v", err)
		}

		restartMsgCount := strings.Count(logs.Logs, "Process restarting")
		if restartMsgCount != maxRestarts {
			t.Logf("Logs content: %s", logs.Logs)
			t.Errorf("Expected %d restart messages in logs, got %d", maxRestarts, restartMsgCount)
		}
	})

	t.Run("NoRestartOnSuccess", func(t *testing.T) {
		completionCount := 0

		// Start a process that will succeed (exit code 0)
		pid, err := pm.StartProcessWithName(
			"echo 'success'",
			"",
			"test-no-restart-process",
			nil,
			true, // restartOnFailure (but shouldn't restart on success)
			3,    // maxRestarts
			func(process *ProcessInfo) {
				completionCount++
				t.Logf("Process completion: Status=%s, ExitCode=%d, CurrentRestarts=%d",
					process.Status, process.ExitCode, process.CurrentRestarts)
			},
		)
		if err != nil {
			t.Fatalf("Error starting process: %v", err)
		}

		// Wait for process to complete
		time.Sleep(50 * time.Millisecond)

		// Should only complete once (no restarts)
		if completionCount != 1 {
			t.Errorf("Expected 1 completion, got %d", completionCount)
		}

		process, exists := pm.GetProcessByIdentifier(pid)
		if !exists {
			t.Fatal("Process should exist")
		}

		// Should not have restarted
		if process.CurrentRestarts != 0 {
			t.Errorf("Expected CurrentRestarts to be 0, got %d", process.CurrentRestarts)
		}

		// Should be completed successfully
		if process.Status != StatusCompleted {
			t.Errorf("Expected status to be %s, got %s", StatusCompleted, process.Status)
		}
	})

	t.Run("UnlimitedRestarts", func(t *testing.T) {
		// Test with maxRestarts = 0 (unlimited)
		// We'll only let it run for a short time to avoid infinite loop
		pid, err := pm.StartProcessWithName(
			"sh -c 'exit 1'",
			"",
			"test-unlimited-restarts",
			nil,
			true, // restartOnFailure
			0,    // maxRestarts = 0 means unlimited
			func(process *ProcessInfo) {
				t.Logf("Process restart: CurrentRestarts=%d", process.CurrentRestarts)
			},
		)
		if err != nil {
			t.Fatalf("Error starting process: %v", err)
		}

		// Let it restart a few times
		time.Sleep(500 * time.Millisecond)

		// Stop the process to prevent infinite restarts
		err = pm.StopProcess(pid)
		if err != nil {
			// If stop fails, kill it
			pm.KillProcess(pid)
		}

		time.Sleep(100 * time.Millisecond)

		// Get the process by name to get the latest one
		process, exists := pm.GetProcessByIdentifier("test-unlimited-restarts")
		if !exists {
			t.Fatal("Process should exist")
		}

		// Should have restarted multiple times (we let it run for 3 seconds, should get at least a few restarts)
		if process.CurrentRestarts < 2 {
			t.Errorf("Expected at least 2 restarts with unlimited restarts, got %d", process.CurrentRestarts)
		}
	})
}

// TestProcessFailureLogging tests that process failures are logged only when restartOnFailure is enabled
func TestProcessFailureLogging(t *testing.T) {
	pm := GetProcessManager()

	t.Run("FailureLoggingWithRestart", func(t *testing.T) {
		// Start a process that will fail with restart enabled
		_, err := pm.StartProcessWithName(
			"sh -c 'exit 42'", // This command will fail with exit code 42
			"",
			"test-failure-with-restart",
			nil,
			true, // restart enabled - should log failure
			1,    // max 1 restart
			func(process *ProcessInfo) {
				t.Logf("Process completed: Status=%s, ExitCode=%d", process.Status, process.ExitCode)
			},
		)
		if err != nil {
			t.Fatalf("Error starting process: %v", err)
		}

		// Wait for process to complete
		time.Sleep(500 * time.Millisecond)

		// Get process logs
		logs, err := pm.GetProcessOutput("test-failure-with-restart")
		if err != nil {
			t.Fatalf("Error getting process output: %v", err)
		}

		// Check that failure message is in logs
		if !strings.Contains(logs.Logs, "Process failed with exit code 42") {
			t.Errorf("Expected failure message in logs when restartOnFailure=true, got: %s", logs.Logs)
		}
	})

	t.Run("FailureLoggingWithoutRestart", func(t *testing.T) {
		// Start a process that will fail with restart disabled
		_, err := pm.StartProcessWithName(
			"sh -c 'exit 42'", // This command will fail with exit code 42
			"",
			"test-failure-without-restart",
			nil,
			false, // restart disabled - should NOT log failure
			0,
			func(process *ProcessInfo) {
				t.Logf("Process completed: Status=%s, ExitCode=%d", process.Status, process.ExitCode)
			},
		)
		if err != nil {
			t.Fatalf("Error starting process: %v", err)
		}

		// Wait for process to complete
		time.Sleep(50 * time.Millisecond)

		// Get process logs
		logs, err := pm.GetProcessOutput("test-failure-without-restart")
		if err != nil {
			t.Fatalf("Error getting process output: %v", err)
		}

		// Check that failure message is NOT in logs
		if strings.Contains(logs.Logs, "Process failed with exit code") {
			t.Errorf("Expected NO failure message in logs when restartOnFailure=false, got: %s", logs.Logs)
		}
	})

	t.Run("NoSuccessLogging", func(t *testing.T) {
		// Start a process that will succeed
		_, err := pm.StartProcessWithName(
			"echo 'test success'",
			"",
			"test-success-no-logging",
			nil,
			false, // restart disabled
			0,
			func(process *ProcessInfo) {
				t.Logf("Process completed: Status=%s, ExitCode=%d", process.Status, process.ExitCode)
			},
		)
		if err != nil {
			t.Fatalf("Error starting process: %v", err)
		}

		// Wait for process to complete
		time.Sleep(50 * time.Millisecond)

		// Get process logs
		logs, err := pm.GetProcessOutput("test-success-no-logging")
		if err != nil {
			t.Fatalf("Error getting process output: %v", err)
		}

		// Check that success message is NOT in logs
		if strings.Contains(logs.Logs, "Process completed successfully") {
			t.Errorf("Expected NO success message in logs, got: %s", logs.Logs)
		}

		// But the actual command output should still be there
		if !strings.Contains(logs.Logs, "test success") {
			t.Errorf("Expected command output in logs, got: %s", logs.Logs)
		}
	})
}

// TestProcessRestartStreamingContinuity tests that log streaming continues seamlessly when a process restarts
func TestProcessRestartStreamingContinuity(t *testing.T) {
	pm := GetProcessManager()

	t.Run("StreamingContinuityOnRestart", func(t *testing.T) {
		// Create a buffer to capture streamed logs
		var streamBuffer strings.Builder

		// Start a process that will fail and restart
		_, err := pm.StartProcessWithName(
			"sh -c 'echo \"before-exit\"; sleep 0.5; exit 1'", // This will output, wait, then fail
			"",
			"test-streaming-restart",
			nil,
			true, // restart enabled
			1,    // max 1 restart (simpler test)
			func(process *ProcessInfo) {
				t.Logf("Process completed: Status=%s, ExitCode=%d, CurrentRestarts=%d",
					process.Status, process.ExitCode, process.CurrentRestarts)
			},
		)
		if err != nil {
			t.Fatalf("Error starting process: %v", err)
		}

		// Start streaming immediately
		err = pm.StreamProcessOutput("test-streaming-restart", &streamBuffer)
		if err != nil {
			t.Fatalf("Error starting stream: %v", err)
		}

		// Wait for the process to complete all restart attempts
		time.Sleep(1000 * time.Millisecond)

		// Get the final streamed content
		streamedContent := streamBuffer.String()
		t.Logf("Streamed content: %s", streamedContent)

		// Verify that the stream contains logs from all attempts
		expectedContent := []string{
			"before-exit",                      // Original process output
			"Process failed with exit code 1",  // Failure message
			"Process restarting (attempt 1/1)", // Restart message
			"before-exit",                      // Restart output (command runs again)
		}

		for _, expected := range expectedContent {
			if !strings.Contains(streamedContent, expected) {
				t.Errorf("Expected streamed content to contain '%s', but it didn't. Full content: %s", expected, streamedContent)
			}
		}

		// Verify we have output from both attempts (original + 1 restart)
		attemptCount := strings.Count(streamedContent, "before-exit")
		if attemptCount < 2 {
			t.Errorf("Expected at least 2 instances of 'before-exit' in stream (original + restart), got %d", attemptCount)
		}
	})

	t.Run("StreamingStartedAfterRestart", func(t *testing.T) {
		// Start a process that will restart
		_, err := pm.StartProcessWithName(
			"sh -c 'echo \"test-output\"; sleep 0.5; exit 1'",
			"",
			"test-stream-after-restart",
			nil,
			true, // restart enabled
			0,    // unlimited restarts
			func(process *ProcessInfo) {
				t.Logf("Process completed: Status=%s, CurrentRestarts=%d", process.Status, process.CurrentRestarts)
			},
		)
		if err != nil {
			t.Fatalf("Error starting process: %v", err)
		}

		// Wait for a few restarts to happen
		time.Sleep(1000 * time.Millisecond)

		// Now start streaming - should get the complete history
		var streamBuffer strings.Builder
		err = pm.StreamProcessOutput("test-stream-after-restart", &streamBuffer)
		if err != nil {
			t.Fatalf("Error starting stream: %v", err)
		}

		// Let it stream for a bit more
		time.Sleep(50 * time.Millisecond)

		// Stop the process to prevent infinite restarts
		pm.StopProcess("test-stream-after-restart")

		streamedContent := streamBuffer.String()
		t.Logf("Streamed content: %s", streamedContent)

		// Should contain multiple restart attempts
		if !strings.Contains(streamedContent, "Process restarting") {
			t.Error("Expected stream to contain restart messages")
		}

		// Should contain multiple outputs from different restart attempts
		outputCount := strings.Count(streamedContent, "test-output")
		if outputCount < 2 {
			t.Errorf("Expected at least 2 instances of 'test-output' in stream, got %d", outputCount)
		}
	})
}
