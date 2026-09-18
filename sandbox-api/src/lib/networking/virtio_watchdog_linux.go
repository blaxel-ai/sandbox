//go:build linux

package networking

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

// StartVirtioWatchdog watches the kernel log for a virtio_net ring that the
// device and the driver no longer agree on, and resyncs the driver with the
// device.
//
// The desync shows up after a snapshot/restore cycle: the device's used index
// lags behind what the driver already consumed, the driver reads a stale
// used-ring slot ("id N is not a head!") and marks the queue broken for good,
// so every packet the host queues on it is dropped and the sandbox is
// unreachable although the guest keeps running. Nothing in userspace clears
// that flag, and the usual way out - resetting the device by rebinding the
// driver - is not available on Firecracker, which turns a reset of an
// activated net device into a permanent FAILED status. The device itself is
// fine and keeps running on the same rings though, so the watchdog loads a
// small kernel module (kmod/virtio_ring_resync) that fixes the driver's view:
// it drops the stale slots, re-arms the notification and clears the broken
// flag. The interface, its addresses and routes are untouched.
//
// It returns immediately; the watchdog runs until ctx is cancelled.
func StartVirtioWatchdog(ctx context.Context) {
	if VirtioWatchdogDisabled(os.Getenv) {
		logrus.Infof("[VirtioWatchdog] Disabled by %s, not watching", EnvDisableVirtioWatchdog)
		return
	}
	if _, err := os.Stat(virtioDevicesDir); err != nil {
		logrus.Debugf("[VirtioWatchdog] No virtio bus on this kernel, not watching (%v)", err)
		return
	}
	f, err := os.Open(kmsgPath)
	if err != nil {
		logrus.WithError(err).Warn("[VirtioWatchdog] Cannot read the kernel log, a broken virtio_net ring will not be recovered")
		return
	}
	// Read from the start of the ring buffer: the kernel logs a broken queue
	// exactly once, so a line that predates this process (sandbox-api restarted
	// after the ring broke) is still worth acting on. Recovering a queue that
	// is not broken is a no-op.
	recover := func(device string) error { return errors.New("no resync module in this build") }
	if module, err := loadableResyncModule(); err != nil {
		logrus.WithError(err).Warn("[VirtioWatchdog] Resync module unavailable, a broken virtio_net ring will only be reported")
	} else {
		recover = module.recover
	}
	go watchKmsg(ctx, f, newRecoveryGate(virtioRecoverCooldown, time.Now), recover)
	logrus.Info("[VirtioWatchdog] Watching the kernel log for a broken virtio_net ring")
}

func watchKmsg(ctx context.Context, f *os.File, gate *recoveryGate, recover func(device string) error) {
	defer func() { _ = f.Close() }()
	go func() {
		<-ctx.Done()
		_ = f.Close()
	}()

	// /dev/kmsg hands out one record per read; a Reader with a large buffer
	// keeps that boundary. A record the kernel dropped from the ring buffer
	// surfaces as EPIPE on read, which is not a reason to stop.
	r := bufio.NewReaderSize(f, 8192)
	for {
		line, err := r.ReadString('\n')
		if device := parseKmsgLine(line); device != "" {
			if gate.allow(device) {
				logrus.Warnf("[VirtioWatchdog] Kernel reports a broken virtio_net ring on %s: %s", device, strings.TrimSpace(line))
				go recoverWithRetries(ctx, device, recover)
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if isKmsgOverrun(err) {
				continue
			}
			logrus.WithError(err).Warn("[VirtioWatchdog] Kernel log read failed, stopping")
			return
		}
	}
}

var virtioRecoverRetryDelay = time.Second

// recoverWithRetries runs recover until it succeeds or the attempts are used
// up. The kernel logs the broken ring once and then stays silent on it, so a
// failed attempt has no second trigger to fall back on.
func recoverWithRetries(ctx context.Context, device string, recover func(device string) error) {
	delay := virtioRecoverRetryDelay
	for attempt := 1; ; attempt++ {
		err := recover(device)
		if err == nil {
			return
		}
		if attempt == virtioRecoverAttempts {
			logrus.WithError(err).Errorf("[VirtioWatchdog] Failed to recover %s after %d attempts, the sandbox network may stay unreachable", device, attempt)
			return
		}
		logrus.WithError(err).Warnf("[VirtioWatchdog] Recovering %s failed (attempt %d/%d), retrying in %s", device, attempt, virtioRecoverAttempts, delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay *= 2
	}
}

// resyncKmod is the embedded virtio_ring_resync kernel module, checked against
// the running kernel.
type resyncKmod struct {
	image []byte
	mu    sync.Mutex
}

// loadableResyncModule returns the embedded module if this build ships one and
// the running kernel is the one it was built for.
func loadableResyncModule() (*resyncKmod, error) {
	image, err := resyncModule.ReadFile(resyncModulePath)
	if err != nil {
		return nil, errors.New("this build does not ship the kernel module")
	}
	targetFile, err := resyncModule.ReadFile(resyncModuleKernel)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", resyncModuleKernel, err)
	}
	target, ok := parseKernelTarget(string(targetFile))
	if !ok {
		return nil, fmt.Errorf("malformed %s", resyncModuleKernel)
	}

	var uts unix.Utsname
	if err := unix.Uname(&uts); err != nil {
		return nil, fmt.Errorf("uname: %w", err)
	}
	release := unix.ByteSliceToString(uts.Release[:])
	config, err := openKernelConfig()
	if err != nil {
		return nil, err
	}
	defer config.Close()
	if !target.matches(release, config) {
		return nil, fmt.Errorf("module built for kernel %s (config %.12s), running %s with another config", target.release, target.configHash, release)
	}
	return &resyncKmod{image: image}, nil
}

// openKernelConfig returns the running kernel's configuration (/proc/config.gz).
func openKernelConfig() (io.ReadCloser, error) {
	f, err := os.Open("/proc/config.gz")
	if err != nil {
		return nil, fmt.Errorf("the kernel does not expose its config: %w", err)
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("reading /proc/config.gz: %w", err)
	}
	return struct {
		io.Reader
		io.Closer
	}{gz, f}, nil
}

// recover resyncs the queues of the interface backed by device (e.g.
// "virtio0") by loading the module, which does its work in init, and
// unloading it right after.
func (m *resyncKmod) recover(device string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ifname, err := virtioNetIface(device)
	if err != nil {
		return err
	}
	logrus.WithFields(logrus.Fields{"device": device, "iface": ifname}).Info("[VirtioWatchdog] Resyncing the virtio rings")

	fd, err := unix.MemfdCreate(resyncModuleName, unix.MFD_CLOEXEC)
	if err != nil {
		return fmt.Errorf("memfd for the module: %w", err)
	}
	defer unix.Close(fd)
	if _, err := unix.Write(fd, m.image); err != nil {
		return fmt.Errorf("writing the module: %w", err)
	}

	// Vermagic and symbol CRCs are ignored: the module is built from the same
	// source with the same config as the running kernel (checked at startup)
	// but not from the same tree, so its vermagic lacks the "+" and its CRCs
	// are those of a tree that never had a Module.symvers.
	const flags = unix.MODULE_INIT_IGNORE_MODVERSIONS | unix.MODULE_INIT_IGNORE_VERMAGIC
	params := "netdev=" + ifname
	err = unix.FinitModule(fd, params, flags)
	if errors.Is(err, unix.EEXIST) {
		// Left over from a previous run that could not unload it.
		if err := unix.DeleteModule(resyncModuleName, 0); err != nil {
			return fmt.Errorf("unloading the stale module: %w", err)
		}
		err = unix.FinitModule(fd, params, flags)
	}
	switch {
	case err == nil:
	case errors.Is(err, unix.ENODATA):
		// init found no broken queue: the kernel line was for a queue that
		// recovered on its own or the resync of a previous line covered it.
		// (ENOENT would be ambiguous: the loader uses it for unresolved symbols.)
		logrus.Infof("[VirtioWatchdog] No broken queue left on %s", ifname)
		return nil
	case errors.Is(err, unix.EPROTO):
		return fmt.Errorf("the kernel's virtqueue layout is not the one the module expects, refusing to touch it")
	case errors.Is(err, unix.ENODEV):
		return fmt.Errorf("%s not found by the kernel", ifname)
	default:
		return fmt.Errorf("loading %s: %w", resyncModuleName, err)
	}

	resynced, _ := os.ReadFile(filepath.Join("/sys/module", resyncModuleName, "parameters", "resynced"))
	if err := unix.DeleteModule(resyncModuleName, 0); err != nil {
		logrus.WithError(err).Warnf("[VirtioWatchdog] Cannot unload %s, will retry on the next run", resyncModuleName)
	}
	logrus.Infof("[VirtioWatchdog] %s recovered on %s (%s queue(s) resynced)", ifname, device, strings.TrimSpace(string(resynced)))
	return nil
}

// virtioNetIface returns the name of the network interface backed by a virtio
// device, from /sys/bus/virtio/devices/<device>/net/<ifname>.
func virtioNetIface(device string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(virtioDevicesDir, device, "net"))
	if err != nil {
		return "", fmt.Errorf("listing the interface of %s: %w", device, err)
	}
	if len(entries) != 1 {
		return "", fmt.Errorf("%s backs %d interfaces, expected 1", device, len(entries))
	}
	return entries[0].Name(), nil
}

// isKmsgOverrun reports whether a /dev/kmsg read failed because records were
// overwritten in the kernel ring buffer before they were read (EPIPE); the
// next read resumes at the oldest available record.
func isKmsgOverrun(err error) bool {
	return errors.Is(err, syscall.EPIPE)
}
