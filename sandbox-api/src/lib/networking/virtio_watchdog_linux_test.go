//go:build linux

package networking

import (
	"context"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
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
	go watchKmsg(ctx, r, newRecoveryGate(time.Hour, time.Now), func(device string) error {
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

func TestRestorableRoutesDropsKernelManagedOnes(t *testing.T) {
	_, dst, _ := net.ParseCIDR("172.16.0.0/24")
	routes := []netlink.Route{
		{Table: syscall.RT_TABLE_LOCAL, Protocol: syscall.RTPROT_KERNEL, Dst: dst},
		{Table: syscall.RT_TABLE_MAIN, Protocol: syscall.RTPROT_KERNEL, Dst: dst},
		{Table: syscall.RT_TABLE_MAIN, Protocol: syscall.RTPROT_BOOT, Gw: net.IPv4(172, 16, 0, 1)},
		{Protocol: syscall.RTPROT_STATIC, Dst: dst},
	}
	got := restorableRoutes(routes)
	if len(got) != 2 || got[0].Gw == nil || got[1].Protocol != syscall.RTPROT_STATIC {
		t.Fatalf("expected the two user routes, got %v", got)
	}
}
