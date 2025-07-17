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
		_, err := pm.StartProcessWithName("sleep 5", "", name, nil, func(process *ProcessInfo) {
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
		_, err := pm.StartProcessWithName("echo '"+expectedOutput+"'", "", name, nil, func(process *ProcessInfo) {
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
		_, err := pm.StartProcessWithName("ls -la", "", name, nil, func(process *ProcessInfo) {
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
		_, err := pm.StartProcessWithName("sleep 1", "", name, nil, func(process *ProcessInfo) {
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
