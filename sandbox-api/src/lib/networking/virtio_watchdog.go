package networking

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// EnvEnableVirtioWatchdog turns the virtio_net watchdog on when set to a
	// truthy value at boot. It is off by default.
	EnvEnableVirtioWatchdog = "BL_ENABLE_VIRTIO_WATCHDOG"

	kmsgPath              = "/dev/kmsg"
	virtioDevicesDir      = "/sys/bus/virtio/devices"
	virtioNetDriverDir    = "/sys/bus/virtio/drivers/virtio_net"
	virtioRecoverCooldown = 30 * time.Second
	virtioRebindSettle    = 500 * time.Millisecond
	virtioRebindAttempts  = 5
)

// virtioRingBroken matches the kernel message a virtio_net driver emits when
// the device hands it a descriptor it never posted: the guest and the host
// disagree on the state of the rx ring, and no packet gets through anymore.
//
//	virtio_net virtio0: input.0:id 171 is not a head!
var virtioRingBroken = regexp.MustCompile(`virtio_net (virtio\d+): [^:]*:id \d+ is not a head!`)

// VirtioWatchdogEnabled reports whether the watchdog is opted in via the
// BL_ENABLE_VIRTIO_WATCHDOG environment variable.
func VirtioWatchdogEnabled(env func(string) string) bool {
	v, err := strconv.ParseBool(strings.TrimSpace(env(EnvEnableVirtioWatchdog)))
	return err == nil && v
}

// parseKmsgLine extracts the virtio device (e.g. "virtio0") from a /dev/kmsg
// record reporting a broken virtio_net ring. It returns "" for any other
// record.
//
// A /dev/kmsg record is "<prio>,<seq>,<usec>,<flags>[,...];<message>", possibly
// followed by continuation lines starting with a space.
func parseKmsgLine(line string) string {
	msg := line
	if i := strings.IndexByte(line, ';'); i >= 0 {
		msg = line[i+1:]
	}
	m := virtioRingBroken.FindStringSubmatch(msg)
	if m == nil {
		return ""
	}
	return m[1]
}

// recoveryGate rate-limits recoveries per device: the kernel logs one "is not
// a head" line per bad descriptor, so a single incident produces a burst of
// them, and a rebind must not be re-triggered by the tail of the burst.
type recoveryGate struct {
	cooldown time.Duration
	now      func() time.Time
	last     map[string]time.Time
}

func newRecoveryGate(cooldown time.Duration, now func() time.Time) *recoveryGate {
	return &recoveryGate{cooldown: cooldown, now: now, last: map[string]time.Time{}}
}

// allow reports whether a recovery may run for device now, and records it.
func (g *recoveryGate) allow(device string) bool {
	t := g.now()
	if last, ok := g.last[device]; ok && t.Sub(last) < g.cooldown {
		return false
	}
	g.last[device] = t
	return true
}
