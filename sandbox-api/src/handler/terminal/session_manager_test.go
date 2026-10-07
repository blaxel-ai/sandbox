package terminal

import (
	"os"
	"strings"
	"testing"
	"time"
)

func pipeSession(t *testing.T) (*ManagedSession, *os.File, chan struct{}) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	shellDone := make(chan struct{})
	ms := newManagedSession(t.Name(), &TerminalSession{ptmx: reader, closeCh: make(chan struct{}), shellDoneCh: shellDone})
	t.Cleanup(func() { writer.Close(); ms.Close() })
	return ms, writer, shellDone
}

func TestShellExitDrainsFinalOutput(t *testing.T) {
	ms, writer, shellDone := pipeSession(t)
	sub := ms.Subscribe()
	defer ms.Unsubscribe(sub)
	close(shellDone)
	// A shell can exit before the reader has consumed its last bytes.
	time.Sleep(20 * time.Millisecond)
	if _, err := writer.Write([]byte("last output\n")); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	select {
	case <-ms.Done():
	case <-time.After(time.Second):
		t.Fatal("session did not end")
	}
	if got := string(ms.GetBuffer()); !strings.Contains(got, "last output\n") {
		t.Fatalf("final output lost: %q", got)
	}
	select {
	case got := <-sub.Ch:
		if string(got) != "last output\n" {
			t.Fatalf("unexpected output %q", got)
		}
	default:
		t.Fatal("final output not published before Done")
	}
}

func TestPTYEOFClosesSession(t *testing.T) {
	ms, writer, _ := pipeSession(t)
	writer.Close()
	select {
	case <-ms.Session.Done():
	case <-time.After(time.Second):
		t.Fatal("PTY EOF did not release session resources")
	}
}

func TestShellExitClosesPTYHeldByChild(t *testing.T) {
	ms, _, shellDone := pipeSession(t)
	close(shellDone)
	select {
	case <-ms.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("open slave prevented session completion")
	}
	select {
	case <-ms.Session.Done():
	case <-time.After(time.Second):
		t.Fatal("PTY was not closed")
	}
}
