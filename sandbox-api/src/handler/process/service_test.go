package process

import (
	"testing"
)

// TestExecuteProcessWithWaitAndRestart tests the fix for PID handling when
// waitForCompletion=true and restartOnFailure=true
func TestExecuteProcessWithWaitAndRestart(t *testing.T) {
	pm := GetProcessManager()

	t.Run("WaitForCompletionWithRestart", func(t *testing.T) {
		// Execute a process that will fail and restart, with waitForCompletion=true
		processInfo, err := pm.ExecuteProcess(
			"sh -c 'echo test-output; exit 1'", // Command that will fail
			"",                                 // workingDir
			"test-wait-and-restart",            // name
			nil,                                // env
			true,                               // waitForCompletion
			5,                                  // timeout
			[]int{},                            // waitForPorts
			true,                               // restartOnFailure
			2,                                  // maxRestarts
		)

		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// Verify that we got the final process info
		if processInfo.PID == "" {
			t.Error("Expected a valid PID, got empty string")
		}

		// Verify that the process has the expected restart count
		if processInfo.CurrentRestarts != 2 {
			t.Errorf("Expected CurrentRestarts to be 2, got %d", processInfo.CurrentRestarts)
		}

		// Verify that the process status is failed (after exhausting restarts)
		if processInfo.Status != "failed" {
			t.Errorf("Expected status to be 'failed', got '%s'", processInfo.Status)
		}

		// Verify that we can retrieve the process using the returned PID
		retrievedProcess, exists := pm.GetProcessByIdentifier(processInfo.PID)
		if !exists {
			t.Errorf("Could not retrieve process using returned PID %s", processInfo.PID)
		}

		// Verify that the retrieved process has the same data
		if retrievedProcess.CurrentRestarts != processInfo.CurrentRestarts {
			t.Errorf("Retrieved process has different restart count: expected %d, got %d",
				processInfo.CurrentRestarts, retrievedProcess.CurrentRestarts)
		}

		// Verify that logs contain evidence of all restart attempts
		if retrievedProcess.logs == nil {
			t.Error("Expected logs to be present")
		} else {
			logsContent := retrievedProcess.logs.String()

			// Should have 3 instances of "test-output" (original + 2 restarts)
			outputCount := countSubstring(logsContent, "test-output")
			if outputCount != 3 {
				t.Errorf("Expected 3 instances of 'test-output' in logs, got %d. Logs: %s", outputCount, logsContent)
			}

			// Should have 2 restart messages
			restartCount := countSubstring(logsContent, "Process restarting")
			if restartCount != 2 {
				t.Errorf("Expected 2 restart messages in logs, got %d. Logs: %s", restartCount, logsContent)
			}
		}
	})

	t.Run("WaitForCompletionWithSuccess", func(t *testing.T) {
		// Test that waitForCompletion works correctly with successful processes too
		processInfo, err := pm.ExecuteProcess(
			"echo 'success'",    // Command that will succeed
			"",                  // workingDir
			"test-wait-success", // name
			nil,                 // env
			true,                // waitForCompletion
			5,                   // timeout
			[]int{},             // waitForPorts
			true,                // restartOnFailure (shouldn't matter for success)
			3,                   // maxRestarts
		)

		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// Verify that the process completed successfully
		if processInfo.Status != "completed" {
			t.Errorf("Expected status to be 'completed', got '%s'", processInfo.Status)
		}

		// Verify that no restarts occurred
		if processInfo.CurrentRestarts != 0 {
			t.Errorf("Expected CurrentRestarts to be 0, got %d", processInfo.CurrentRestarts)
		}

		// Verify that we can retrieve the process using the returned PID
		_, exists := pm.GetProcessByIdentifier(processInfo.PID)
		if !exists {
			t.Errorf("Could not retrieve process using returned PID %s", processInfo.PID)
		}
	})
}

// Helper function to count substring occurrences
func countSubstring(text, substr string) int {
	count := 0
	start := 0
	for {
		pos := indexOf(text[start:], substr)
		if pos == -1 {
			break
		}
		count++
		start += pos + len(substr)
	}
	return count
}

// Helper function to find index of substring
func indexOf(text, substr string) int {
	for i := 0; i <= len(text)-len(substr); i++ {
		if text[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// TestMaxRestartsValidation tests that maxRestarts cannot exceed 25
func TestMaxRestartsValidation(t *testing.T) {
	pm := GetProcessManager()

	t.Run("MaxRestartsExceedsLimit", func(t *testing.T) {
		// Try to create a process with maxRestarts > 25
		_, err := pm.ExecuteProcess(
			"echo test",         // command
			"",                  // workingDir
			"test-max-restarts", // name
			nil,                 // env
			false,               // waitForCompletion
			5,                   // timeout
			[]int{},             // waitForPorts
			true,                // restartOnFailure
			26,                  // maxRestarts > 25
		)

		// Should return an error
		if err == nil {
			t.Fatal("Expected error when maxRestarts > 25, got nil")
		}

		expectedError := "maxRestarts cannot exceed 25, got 26"
		if err.Error() != expectedError {
			t.Errorf("Expected error '%s', got '%s'", expectedError, err.Error())
		}
	})

	t.Run("MaxRestartsAtLimit", func(t *testing.T) {
		// Try to create a process with maxRestarts = 25 (should work)
		processInfo, err := pm.ExecuteProcess(
			"echo test",            // command
			"",                     // workingDir
			"test-max-restarts-25", // name
			nil,                    // env
			false,                  // waitForCompletion
			5,                      // timeout
			[]int{},                // waitForPorts
			true,                   // restartOnFailure
			25,                     // maxRestarts = 25
		)

		// Should not return an error
		if err != nil {
			t.Fatalf("Expected no error when maxRestarts = 25, got: %v", err)
		}

		if processInfo == nil {
			t.Fatal("Expected processInfo, got nil")
		}

		if processInfo.MaxRestarts != 25 {
			t.Errorf("Expected MaxRestarts to be 25, got %d", processInfo.MaxRestarts)
		}
	})

	t.Run("MaxRestartsUnderLimit", func(t *testing.T) {
		// Try to create a process with maxRestarts < 25 (should work)
		processInfo, err := pm.ExecuteProcess(
			"echo test",            // command
			"",                     // workingDir
			"test-max-restarts-10", // name
			nil,                    // env
			false,                  // waitForCompletion
			5,                      // timeout
			[]int{},                // waitForPorts
			true,                   // restartOnFailure
			10,                     // maxRestarts = 10
		)

		// Should not return an error
		if err != nil {
			t.Fatalf("Expected no error when maxRestarts = 10, got: %v", err)
		}

		if processInfo == nil {
			t.Fatal("Expected processInfo, got nil")
		}

		if processInfo.MaxRestarts != 10 {
			t.Errorf("Expected MaxRestarts to be 10, got %d", processInfo.MaxRestarts)
		}
	})

	t.Run("MaxRestartsZero", func(t *testing.T) {
		// Try to create a process with maxRestarts = 0 (should be converted to 25)
		processInfo, err := pm.ExecuteProcess(
			"echo test",           // command
			"",                    // workingDir
			"test-max-restarts-0", // name
			nil,                   // env
			false,                 // waitForCompletion
			5,                     // timeout
			[]int{},               // waitForPorts
			true,                  // restartOnFailure
			0,                     // maxRestarts = 0 (should be converted to 25)
		)

		// Should not return an error
		if err != nil {
			t.Fatalf("Expected no error when maxRestarts = 0, got: %v", err)
		}

		if processInfo == nil {
			t.Fatal("Expected processInfo, got nil")
		}

		// maxRestarts = 0 should be converted to 25
		if processInfo.MaxRestarts != 25 {
			t.Errorf("Expected MaxRestarts to be 25 (converted from 0), got %d", processInfo.MaxRestarts)
		}
	})
}
