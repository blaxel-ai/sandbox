package process

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// padded returns script preceded by a comment line, size bytes long in total.
func padded(t *testing.T, script string, size int) string {
	t.Helper()
	command := "#" + strings.Repeat("x", size-len(script)-2) + "\n" + script
	if len(command) != size {
		t.Fatalf("padded command is %d bytes, want %d", len(command), size)
	}
	return command
}

// runToCompletion starts command and returns the finished process and its logs.
func runToCompletion(t *testing.T, command, workingDir string, env map[string]string) (*ProcessInfo, ProcessLogs) {
	t.Helper()
	pm := GetProcessManager()
	finished := make(chan *ProcessInfo, 1)
	pid, err := pm.StartProcess(command, workingDir, env, false, 0, false, 0, false, func(p *ProcessInfo) {
		finished <- p
	})
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	select {
	case p := <-finished:
		logs, err := pm.GetProcessOutput(pid)
		if err != nil {
			t.Fatalf("GetProcessOutput: %v", err)
		}
		return p, logs
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for process to finish")
		return nil, ProcessLogs{}
	}
}

func TestLargeCommandRunsInFull(t *testing.T) {
	for _, shell := range []string{"sh", "bash"} {
		t.Run(shell, func(t *testing.T) {
			t.Setenv("SHELL", shell)
			command := "echo start\nX='" + strings.Repeat("a", 204800) + "'\necho ${#X} | tr -d ' '\necho end"

			p, logs := runToCompletion(t, command, "", nil)

			if p.Status != StatusCompleted || p.ExitCode != 0 {
				t.Fatalf("status=%s exit=%d stderr=%q", p.Status, p.ExitCode, logs.Stderr)
			}
			if logs.Stdout != "start\n204800\nend\n" {
				t.Errorf("stdout = %q", logs.Stdout)
			}
		})
	}
}

func TestCommandsAroundTheArgvLimitRun(t *testing.T) {
	t.Setenv("SHELL", "sh")
	for _, size := range []int{131071, 131072, 131073} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			p, logs := runToCompletion(t, padded(t, "echo ok", size), "", nil)

			if p.ExitCode != 0 || logs.Stdout != "ok\n" {
				t.Errorf("size %d: exit=%d stdout=%q stderr=%q", size, p.ExitCode, logs.Stdout, logs.Stderr)
			}
		})
	}
}

func TestLargeCommandPropagatesExitCode(t *testing.T) {
	t.Setenv("SHELL", "sh")
	p, _ := runToCompletion(t, padded(t, "false || exit 7\necho unreachable", 200*1024), "", nil)

	if p.Status != StatusFailed || p.ExitCode != 7 {
		t.Errorf("status=%s exit=%d, want failed/7", p.Status, p.ExitCode)
	}
}

func TestLargeCommandReportsStatusOfLastCommand(t *testing.T) {
	t.Setenv("SHELL", "sh")
	p, _ := runToCompletion(t, padded(t, "true\nsh -c 'exit 3'", 200*1024), "", nil)

	if p.ExitCode != 3 {
		t.Errorf("exit=%d, want 3", p.ExitCode)
	}
}

func TestLargeCommandKeepsWorkingDirAndEnv(t *testing.T) {
	t.Setenv("SHELL", "sh")
	_, logs := runToCompletion(t, padded(t, "pwd; echo \"$FOO\"", 200*1024), "/tmp", map[string]string{"FOO": "bar baz"})

	if logs.Stdout != "/tmp\nbar baz\n" {
		t.Errorf("stdout = %q", logs.Stdout)
	}
}

func TestLargeCommandSeesSameShellNameAsSmallCommand(t *testing.T) {
	t.Setenv("SHELL", "sh")
	_, small := runToCompletion(t, "echo $0", "", nil)
	_, large := runToCompletion(t, padded(t, "echo $0", 200*1024), "", nil)

	if small.Stdout != "sh\n" || large.Stdout != "sh\n" {
		t.Errorf("small $0 = %q, large $0 = %q, want both \"sh\\n\"", small.Stdout, large.Stdout)
	}
}

func TestLargeCommandDoesNotReadItselfFromStdin(t *testing.T) {
	t.Setenv("SHELL", "sh")
	_, logs := runToCompletion(t, padded(t, "cat\necho end", 200*1024), "", nil)

	if logs.Stdout != "end\n" {
		t.Errorf("stdout = %q", logs.Stdout)
	}
}

func TestLargeCommandChildrenDoNotInheritTheScriptPipe(t *testing.T) {
	t.Setenv("SHELL", "sh")
	_, logs := runToCompletion(t, padded(t, "sh -c 'test -e /proc/self/fd/3 && echo leaked || echo clean'", 200*1024), "", nil)

	if logs.Stdout != "clean\n" {
		t.Errorf("stdout = %q", logs.Stdout)
	}
}

func TestLargeCommandReadsStreamedStdin(t *testing.T) {
	t.Setenv("SHELL", "sh")
	pm := GetProcessManager()
	finished := make(chan *ProcessInfo, 1)
	pid, err := pm.StartProcess(padded(t, "read line; echo \"got:$line\"; cat", 200*1024), "", nil, false, 0, false, 0, true, func(p *ProcessInfo) {
		finished <- p
	})
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	if err := pm.WriteStdin(pid, []byte("hello\nrest\n")); err != nil {
		t.Fatalf("WriteStdin: %v", err)
	}
	if err := pm.CloseStdin(pid); err != nil {
		t.Fatalf("CloseStdin: %v", err)
	}
	select {
	case p := <-finished:
		logs, _ := pm.GetProcessOutput(pid)
		if p.ExitCode != 0 || logs.Stdout != "got:hello\nrest\n" {
			t.Errorf("exit=%d stdout=%q stderr=%q", p.ExitCode, logs.Stdout, logs.Stderr)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for process to finish")
	}
}

func TestLargeCommandIsRerunInFullOnRestart(t *testing.T) {
	t.Setenv("SHELL", "sh")
	pm := GetProcessManager()
	finished := make(chan *ProcessInfo, 1)
	pid, err := pm.StartProcess(padded(t, "echo run; exit 1", 200*1024), "", nil, true, 1, false, 0, false, func(p *ProcessInfo) {
		finished <- p
	})
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	select {
	case p := <-finished:
		logs, _ := pm.GetProcessOutput(pid)
		if p.RestartCount != 1 || p.ExitCode != 1 || strings.Count(logs.Stdout, "run\n") != 2 {
			t.Errorf("restarts=%d exit=%d stdout=%q stderr=%q", p.RestartCount, p.ExitCode, logs.Stdout, logs.Stderr)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for process to finish")
	}
}

func TestRunningLargeCommandIsRecognisedAfterARestart(t *testing.T) {
	t.Setenv("SHELL", "sh")
	pm := GetProcessManager()
	command := padded(t, "sleep 30", 200*1024)
	pid, err := pm.StartProcess(command, "", nil, false, 0, false, 0, false, nil)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	t.Cleanup(func() { _ = pm.KillProcess(pid) })
	p, _ := pm.GetProcessByIdentifier(pid)

	if !verifyProcessCommand(p.ProcessPid, command) {
		t.Error("a running large command must be adopted, not marked as a PID reuse")
	}
}
