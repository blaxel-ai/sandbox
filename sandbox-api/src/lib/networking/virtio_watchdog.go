package networking

import (
	"bufio"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// EnvDisableVirtioWatchdog turns the virtio_net watchdog off when set to a
	// truthy value at boot. It is on by default.
	EnvDisableVirtioWatchdog = "BL_DISABLE_VIRTIO_WATCHDOG"

	kmsgPath              = "/dev/kmsg"
	virtioDevicesDir      = "/sys/bus/virtio/devices"
	virtioRecoverCooldown = 2 * time.Second
	virtioRecoverAttempts = 5

	resyncModuleName   = "virtio_ring_resync"
	resyncModulePath   = "kmod/" + resyncModuleName + ".ko"
	resyncModuleKernel = "kmod/" + resyncModuleName + ".kernel"
)

// resyncModule holds the kernel module that resyncs a broken virtqueue with
// its device, built by the Dockerfile against the guest kernel the sandboxes
// run (see kmod/). A build without it (go build on a workstation) only detects
// the problem.
//
//go:embed kmod
var resyncModule embed.FS

// virtioRingBroken matches the kernel message a virtio_net driver emits when
// the device hands it a descriptor it never posted: the guest and the host
// disagree on the state of the rx ring, and no packet gets through anymore.
//
//	virtio_net virtio0: input.0:id 171 is not a head!
var virtioRingBroken = regexp.MustCompile(`virtio_net (virtio\d+): [^:]*:id \d+ is not a head!`)

// VirtioWatchdogDisabled reports whether the watchdog is opted out via the
// BL_DISABLE_VIRTIO_WATCHDOG environment variable.
func VirtioWatchdogDisabled(env func(string) string) bool {
	v, err := strconv.ParseBool(strings.TrimSpace(env(EnvDisableVirtioWatchdog)))
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

// kernelTarget is the kernel a resync module was built for: the release it
// was built from and a digest of the configuration it was built with. The
// module pokes at private driver structures, so it must only be loaded into a
// kernel with the same layout.
type kernelTarget struct {
	release    string
	configHash string
}

// parseKernelTarget reads the "<release>\n<sha256 of config>" file written next
// to the module at build time.
func parseKernelTarget(s string) (kernelTarget, bool) {
	fields := strings.Fields(s)
	if len(fields) != 2 || len(fields[1]) != sha256.Size*2 {
		return kernelTarget{}, false
	}
	return kernelTarget{release: fields[0], configHash: fields[1]}, true
}

// matches reports whether a running kernel (its uname release and the
// contents of its /proc/config.gz) is the one the module was built for. The
// release is compared without the "+" localversion, which only tells the
// kernel was built from a tree with uncommitted changes.
func (k kernelTarget) matches(release string, config io.Reader) bool {
	if strings.TrimSuffix(strings.TrimSpace(release), "+") != k.release {
		return false
	}
	return hashKernelConfig(config) == k.configHash
}

// hashKernelConfig digests a kernel .config the way
// `grep -v '^#' | grep . | sha256sum` does: comments and blank lines dropped,
// every remaining line followed by "\n".
func hashKernelConfig(r io.Reader) string {
	h := sha256.New()
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		h.Write([]byte(line))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}
