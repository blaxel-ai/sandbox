//go:build linux

package networking

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestWatchKmsgRecoversOncePerBurst(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	recovered := make(chan string, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	noReopen := func() (*os.File, error) { return nil, errors.New("closed") }
	go watchKmsg(ctx, r, noReopen, newRecoveryGate(time.Hour, time.Now), func(device string) error {
		recovered <- device
		return nil
	})

	lines := []string{
		"6,1,1,-;random: crng init done\n",
		"3,2,2,-;virtio_net virtio0: input.0:id 171 is not a head!\n",
		"3,3,3,-;virtio_net virtio0: input.0:id 172 is not a head!\n",
		"3,4,4,-;virtio_net virtio0: input.0:id 173 is not a head!\n",
	}
	for _, l := range lines {
		if _, err := w.WriteString(l); err != nil {
			t.Fatal(err)
		}
	}

	select {
	case d := <-recovered:
		if d != "virtio0" {
			t.Fatalf("recovered %q, want virtio0", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no recovery triggered")
	}
	select {
	case d := <-recovered:
		t.Fatalf("burst triggered a second recovery on %q", d)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestWatchKmsgReopensAfterReadError(t *testing.T) {
	virtioRecoverRetryDelay = time.Millisecond
	defer func() { virtioRecoverRetryDelay = time.Second }()

	r1, w1, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r2, w2, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w2.Close() }()

	recovered := make(chan string, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reopen := func() (*os.File, error) { return r2, nil }
	go watchKmsg(ctx, r1, reopen, newRecoveryGate(0, time.Now), func(device string) error {
		recovered <- device
		return nil
	})

	// EOF on the first log stands in for a read error that is not an overrun
	_ = w1.Close()
	if _, err := w2.WriteString("3,2,2,-;virtio_net virtio1: input.0:id 171 is not a head!\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case d := <-recovered:
		if d != "virtio1" {
			t.Fatalf("recovered %q, want virtio1", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the tailer did not survive the read error")
	}
}

func TestRecoverWithRetriesRetriesUntilSuccess(t *testing.T) {
	virtioRecoverRetryDelay = time.Millisecond
	defer func() { virtioRecoverRetryDelay = time.Second }()
	calls := 0
	recoverWithRetries(context.Background(), "virtio0", func(string) error {
		calls++
		if calls < 3 {
			return errors.New("transient")
		}
		return nil
	})
	if calls != 3 {
		t.Fatalf("recover called %d times, want 3", calls)
	}
}

func TestRecoverWithRetriesGivesUp(t *testing.T) {
	virtioRecoverRetryDelay = time.Millisecond
	defer func() { virtioRecoverRetryDelay = time.Second }()
	calls := 0
	recoverWithRetries(context.Background(), "virtio0", func(string) error {
		calls++
		return errors.New("permanent")
	})
	if calls != virtioRecoverAttempts {
		t.Fatalf("recover called %d times, want %d", calls, virtioRecoverAttempts)
	}
}
