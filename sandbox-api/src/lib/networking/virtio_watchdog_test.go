package networking

import (
	"testing"
	"time"
)

func TestParseKmsgLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		{
			name: "rx ring desync as logged by the kernel",
			line: "3,1234,436251671158,-;virtio_net virtio0: input.0:id 171 is not a head!\n",
			want: "virtio0",
		},
		{
			name: "second virtio device, tx queue",
			line: "3,99,1,-;virtio_net virtio3: output.1:id 7 is not a head!",
			want: "virtio3",
		},
		{
			name: "plain dmesg text without the kmsg header",
			line: "[436251.671158] virtio_net virtio0: input.0:id 171 is not a head!",
			want: "virtio0",
		},
		{
			name: "same message from another virtio driver is not ours to fix",
			line: "3,1,1,-;virtio_blk virtio1: req.0:id 3 is not a head!",
			want: "",
		},
		{
			name: "ordinary virtio_net line",
			line: "6,1,1,-;virtio_net virtio0 eth0: renamed from eth1",
			want: "",
		},
		{
			name: "empty",
			line: "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseKmsgLine(tc.line); got != tc.want {
				t.Fatalf("parseKmsgLine(%q) = %q, want %q", tc.line, got, tc.want)
			}
		})
	}
}

func TestRecoveryGateCoalescesBurstPerDevice(t *testing.T) {
	now := time.Unix(1000, 0)
	gate := newRecoveryGate(30*time.Second, func() time.Time { return now })

	if !gate.allow("virtio0") {
		t.Fatal("first report must trigger a recovery")
	}
	now = now.Add(time.Second)
	if gate.allow("virtio0") {
		t.Fatal("a report inside the cooldown must not trigger another recovery")
	}
	if !gate.allow("virtio1") {
		t.Fatal("another device is gated independently")
	}
	now = now.Add(30 * time.Second)
	if !gate.allow("virtio0") {
		t.Fatal("a report after the cooldown must trigger a recovery again")
	}
}

func TestVirtioWatchdogDisabled(t *testing.T) {
	env := func(v string) func(string) string { return func(string) string { return v } }
	for v, want := range map[string]bool{"": false, "false": false, "0": false, "true": true, "1": true, " TRUE ": true, "yes": false} {
		if got := VirtioWatchdogDisabled(env(v)); got != want {
			t.Errorf("VirtioWatchdogDisabled(%q) = %v, want %v", v, got, want)
		}
	}
}
