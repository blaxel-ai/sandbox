package terminal

import (
	"bytes"
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

func readAll(t *testing.T, sub *Subscriber, want int, timeout time.Duration) []byte {
	t.Helper()
	var got []byte
	deadline := time.After(timeout)
	for len(got) < want {
		select {
		case data := <-sub.Ch:
			got = append(got, data...)
		case <-deadline:
			t.Fatalf("got %d of %d bytes", len(got), want)
		}
	}
	return got
}

func TestSlowSubscriberGetsAllOutput(t *testing.T) {
	ms, writer, _ := pipeSession(t)
	sub := ms.Subscribe()
	defer ms.Unsubscribe(sub)

	// Far more than the subscriber channel and the pipe hold together.
	want := bytes.Repeat([]byte("0123456789abcde\n"), 64*1024)
	go func() { _, _ = writer.Write(want) }()

	// The subscriber is not reading: the reader must wait, not drop.
	time.Sleep(200 * time.Millisecond)
	if got := readAll(t, sub, len(want), 10*time.Second); !bytes.Equal(got, want) {
		t.Fatalf("output lost or reordered: got %d bytes, want %d", len(got), len(want))
	}
}

func TestStuckSubscriberIsDroppedAndToldSo(t *testing.T) {
	defer func(d time.Duration) { subscriberLagTimeout = d }(subscriberLagTimeout)
	subscriberLagTimeout = 100 * time.Millisecond

	ms, writer, _ := pipeSession(t)
	stuck := ms.Subscribe()
	defer ms.Unsubscribe(stuck)
	live := ms.Subscribe()
	defer ms.Unsubscribe(live)

	want := bytes.Repeat([]byte("x"), (subscriberChanSize+8)*4096)
	go func() { _, _ = writer.Write(want) }()
	got := readAll(t, live, len(want), 10*time.Second)
	if !bytes.Equal(got, want) {
		t.Fatalf("live subscriber lost output: got %d bytes, want %d", len(got), len(want))
	}
	select {
	case <-stuck.Dropped():
	case <-time.After(time.Second):
		t.Fatal("stuck subscriber was not told it was dropped")
	}
	if n := ms.ClientCount(); n != 1 {
		t.Fatalf("stuck subscriber still registered: %d clients", n)
	}
	// What it was given is a prefix of the output, without gaps.
	var queued []byte
	for len(stuck.Ch) > 0 {
		queued = append(queued, <-stuck.Ch...)
	}
	if !bytes.HasPrefix(want, queued) {
		t.Fatal("dropped subscriber's queued output has gaps")
	}
}

func TestShellExitWaitsForSlowSubscriber(t *testing.T) {
	ms, writer, shellDone := pipeSession(t)
	sub := ms.Subscribe()
	defer ms.Unsubscribe(sub)

	want := bytes.Repeat([]byte("0123456789abcde\n"), 32*1024)
	go func() { _, _ = writer.Write(want) }()
	// The shell exits while most of its output is still unread.
	time.Sleep(50 * time.Millisecond)
	close(shellDone)
	time.Sleep(4 * shellDrainTimeout)

	if got := readAll(t, sub, len(want), 10*time.Second); !bytes.Equal(got, want) {
		t.Fatalf("output lost at shell exit: got %d bytes, want %d", len(got), len(want))
	}
}
