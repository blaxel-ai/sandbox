package process

import (
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// serveEnv makes the test binary act as a tiny HTTP server, so the success
// path can be tested without depending on python or node being installed.
const serveEnv = "WAITFORPORTS_TEST_SERVE_PORT"

func TestMain(m *testing.M) {
	if port := os.Getenv(serveEnv); port != "" {
		// Never outlive the test run, whatever happens to the test.
		time.AfterFunc(time.Minute, func() { os.Exit(0) })
		l, err := net.Listen("tcp", "0.0.0.0:"+port)
		if err != nil {
			os.Exit(2)
		}
		_ = http.Serve(l, http.NotFoundHandler())
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// A process that exits before opening the ports it was started with can never
// open them. waitForPorts used to keep waiting until the timeout ran out, or
// forever without one, so a dev server that crashed on startup looked like a
// slow one. It must fail as soon as the process is over, saying why.

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("finding a free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func useTempLogDir(t *testing.T) {
	t.Helper()
	original := ProcessLogDir
	ProcessLogDir = t.TempDir()
	t.Cleanup(func() { ProcessLogDir = original })
}

func TestWaitForPortsFailsFastWhenProcessExits(t *testing.T) {
	useTempLogDir(t)
	port := freePort(t)

	cases := []struct {
		name     string
		command  string
		timeout  int
		wantText []string
	}{
		{"exit 1 with timeout", "sh -c 'echo boom >&2; exit 1'", 30, []string{"exited with code 1", "boom"}},
		{"exit 1 without timeout", "sh -c 'echo boom >&2; exit 1'", 0, []string{"exited with code 1", "boom"}},
		{"exit after a delay", "sh -c 'sleep 1; echo late >&2; exit 3'", 30, []string{"exited with code 3", "late"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pm := NewProcessManager()
			type result struct {
				err     error
				elapsed time.Duration
			}
			done := make(chan result, 1)
			start := time.Now()
			go func() {
				_, err := pm.ExecuteProcess(tc.command, "", "", nil, false, tc.timeout, []int{port}, false, 0, false, false)
				done <- result{err, time.Since(start)}
			}()

			var r result
			select {
			case r = <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("waitForPorts kept waiting after the process exited")
			}
			if r.err == nil {
				t.Fatal("expected an error: the port never opened")
			}
			if r.elapsed > 5*time.Second {
				t.Fatalf("took %s to notice the process had exited", r.elapsed)
			}
			for _, want := range tc.wantText {
				if !strings.Contains(r.err.Error(), want) {
					t.Errorf("error %q does not mention %q", r.err.Error(), want)
				}
			}
			if !strings.Contains(r.err.Error(), strconv.Itoa(port)) {
				t.Errorf("error %q does not name the port", r.err.Error())
			}
		})
	}
}

// restartOnFailure means a failed run is not the end: waitForPorts must keep
// waiting through the restarts and only give up once the last one has failed.
func TestWaitForPortsWaitsThroughRestarts(t *testing.T) {
	useTempLogDir(t)
	port := freePort(t)
	pm := NewProcessManager()

	start := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := pm.ExecuteProcess("sh -c 'echo attempt; exit 1'", "", "restarts", nil, false, 60, []int{port}, true, 2, false, false)
		done <- err
	}()

	var err error
	select {
	case err = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("waitForPorts never returned after the restarts were exhausted")
	}
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected an error: every run failed and the port never opened")
	}
	// The manager waits about a second before each restart; returning sooner
	// than two restarts' worth means it gave up on the first failure.
	if elapsed < 2*time.Second {
		t.Fatalf("gave up after %s, before the 2 restarts had run", elapsed)
	}
	if !strings.Contains(err.Error(), "exited with code 1") {
		t.Errorf("error %q does not report the exit code", err.Error())
	}
	proc, ok := pm.GetProcessByIdentifier("restarts")
	if !ok {
		t.Fatal("process record missing")
	}
	if proc.RestartCount != 2 {
		t.Errorf("expected 2 restarts before giving up, got %d", proc.RestartCount)
	}
}

// The success path is unchanged: a process that opens its port is ready.
func TestWaitForPortsStillSucceedsWhenThePortOpens(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("port readiness via ss is exercised on linux")
	}
	useTempLogDir(t)
	port := freePort(t)
	pm := NewProcessManager()

	env := map[string]string{serveEnv: strconv.Itoa(port)}
	proc, err := pm.ExecuteProcess(os.Args[0], "", "server", env, false, 30, []int{port}, false, 0, false, false)
	if err != nil {
		t.Fatalf("expected the server to be ready, got: %v", err)
	}
	t.Cleanup(func() { _ = pm.KillProcess("server") })
	if proc.Status != StatusRunning {
		t.Fatalf("expected the server to be running, got %q", proc.Status)
	}
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 2*time.Second)
	if err != nil {
		t.Fatalf("port %d not reachable after waitForPorts returned: %v", port, err)
	}
	conn.Close()
}

// A clean exit is not treated as a failure (a command may exit 0 after starting
// a server in the background), so exit code 0 keeps waiting as before, and a
// process that exits 0 without anything opening the port still times out.
func TestWaitForPortsKeepsWaitingAfterCleanExit(t *testing.T) {
	useTempLogDir(t)
	port := freePort(t)
	pm := NewProcessManager()

	start := time.Now()
	_, err := pm.ExecuteProcess("sh -c 'echo done'", "", "", nil, false, 2, []int{port}, false, 0, false, false)
	if err == nil || !strings.Contains(err.Error(), "timed out waiting for ports after 2 seconds") {
		t.Fatalf("expected the timeout error after a clean exit, got %v", err)
	}
	if elapsed := time.Since(start); elapsed < 2*time.Second {
		t.Fatalf("gave up after %s on a clean exit; a backgrounded server may still open the port", elapsed)
	}
}

// A process that never opens its port and never exits still times out.
func TestWaitForPortsStillTimesOut(t *testing.T) {
	useTempLogDir(t)
	port := freePort(t)
	pm := NewProcessManager()

	start := time.Now()
	_, err := pm.ExecuteProcess("sleep 30", "", "sleeper", nil, false, 2, []int{port}, false, 0, false, false)
	t.Cleanup(func() { _ = pm.KillProcess("sleeper") })
	if err == nil || !strings.Contains(err.Error(), "timed out waiting for ports after 2 seconds") {
		t.Fatalf("expected the timeout error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed < 2*time.Second || elapsed > 6*time.Second {
		t.Fatalf("timeout took %s, expected about 2s", elapsed)
	}
}
