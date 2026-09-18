package blaxel

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/sirupsen/logrus"
)

const (
	defaultScaleFile = "/uk/libukp/scale_to_zero_disable"

	// EnvDisableKeepAlive disables the keepAlive feature for the whole sandbox
	// when set to a truthy value at boot.
	EnvDisableKeepAlive = "BL_DISABLE_KEEPALIVE"
)

// KeepAliveDisabled reports whether the keepAlive feature is disabled for
// this sandbox via the BL_DISABLE_KEEPALIVE environment variable.
func KeepAliveDisabled() bool {
	v, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(EnvDisableKeepAlive)))
	return err == nil && v
}

// scaleAvailable caches whether the scale file infrastructure is available
var scaleAvailableChecked bool
var scaleAvailable bool

// isScaleAvailable checks if the scale-to-zero infrastructure is present
// Returns false if the scale file directory doesn't exist (debug/test environments)
func isScaleAvailable() bool {
	// Check only once at startup
	if !scaleAvailableChecked {
		scaleAvailableChecked = true
		scaleFile := GetScaleFile()
		dir := scaleFile[:strings.LastIndex(scaleFile, "/")]
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			scaleAvailable = false
			logrus.Infof("[Scale] Scale-to-zero infrastructure not available (running in debug/test mode)")
		} else {
			scaleAvailable = true
		}
	}
	return scaleAvailable
}

// GetScaleFile returns the path to the scale-to-zero control file
func GetScaleFile() string {
	if envPath := os.Getenv("BLAXEL_SCALE_FILE"); envPath != "" {
		return envPath
	}
	return defaultScaleFile
}

// writeWithLock safely writes to the scale file with file locking
func writeWithLock(operation string) error {
	// Skip if scale infrastructure is not available (debug/test environments)
	if !isScaleAvailable() {
		return nil
	}

	scaleFile := GetScaleFile()

	// Open or create the file (same as async-sidecar)
	file, err := os.OpenFile(scaleFile, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		logrus.Debugf("[Scale] Failed to open scale control file (error: %v)", err)
		return err
	}
	defer func() {
		_ = file.Close()
	}()

	// Acquire exclusive lock
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		logrus.Debugf("[Scale] Failed to acquire lock on scale control file (error: %v)", err)
		return err
	}
	defer func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	}()

	// Write the operation
	if _, err := file.WriteString(operation); err != nil {
		logrus.Debugf("[Scale] Failed to write to scale control file (error: %v)", err)
		return err
	}

	return nil
}

// ScaleDisable disables scale-to-zero by incrementing the counter
func ScaleDisable() error {
	if KeepAliveDisabled() {
		logrus.Infof("[Scale] keepAlive is disabled for this sandbox, not touching the scale-to-zero counter")
		return nil
	}
	err := writeWithLock("+")
	if err != nil {
		logrus.Warnf("[Scale] Failed to disable scale-to-zero: %v", err)
		return err
	}
	counter, _ := GetCounter()
	logrus.Infof("[Scale] Disabled scale-to-zero (wrote '+', counter now: %d) - sandbox staying AWAKE", counter)
	return nil
}

// ScaleEnable enables scale-to-zero by decrementing the counter
func ScaleEnable() error {
	if KeepAliveDisabled() {
		return nil
	}
	err := writeWithLock("-")
	if err != nil {
		logrus.Warnf("[Scale] Failed to enable scale-to-zero: %v", err)
		return err
	}
	counter, _ := GetCounter()
	if counter == 0 {
		logrus.Infof("[Scale] Enabled scale-to-zero (wrote '-', counter now: %d) - sandbox can AUTO-HIBERNATE", counter)
	} else {
		logrus.Infof("[Scale] Decremented scale-to-zero (wrote '-', counter now: %d) - still AWAKE", counter)
	}
	return nil
}

// HoldAwake keeps the sandbox from being hibernated for as long as the returned
// release function has not been called.
//
// Unlike ScaleDisable it ignores BL_DISABLE_KEEPALIVE: it exists for the
// operations this API cannot survive being suspended in the middle of - an
// archive export streaming the filesystem out, an import restoring it at boot -
// where a sandbox that opted out of keeping itself alive for its workload would
// otherwise lose the operation, silently and halfway through.
//
// The release runs at most once, and only decrements a counter this call
// actually incremented: releasing a hold that was never taken would hibernate a
// sandbox another hold is keeping awake.
func HoldAwake(reason string) func() {
	held := true
	if err := writeWithLock("+"); err != nil {
		held = false
		logrus.WithError(err).Warnf("[Scale] Failed to keep the sandbox awake for %s: it may be hibernated before it completes", reason)
	} else {
		counter, _ := GetCounter()
		logrus.Infof("[Scale] Keeping the sandbox awake for %s (counter now: %d)", reason, counter)
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			if !held {
				return
			}
			if err := writeWithLock("-"); err != nil {
				logrus.WithError(err).Warnf("[Scale] Failed to release the keep-alive taken for %s", reason)
				return
			}
			counter, _ := GetCounter()
			logrus.Infof("[Scale] Released the keep-alive taken for %s (counter now: %d)", reason, counter)
		})
	}
}

// ScaleReset resets the scale-to-zero counter to 0
// This should be called on startup to handle crash recovery
func ScaleReset() error {
	err := writeWithLock("=0")
	if err != nil {
		logrus.Warnf("[Scale] Failed to reset scale-to-zero: %v", err)
		return err
	}
	logrus.Infof("[Scale] Reset scale-to-zero counter to 0 (wrote '=0') - sandbox can AUTO-HIBERNATE")
	return nil
}

// GetCounter reads the current scale-to-zero counter value
func GetCounter() (int, error) {
	// Return 0 if scale infrastructure is not available
	if !isScaleAvailable() {
		return 0, nil
	}

	scaleFile := GetScaleFile()

	data, err := os.ReadFile(scaleFile)
	if err != nil {
		return -1, err
	}

	content := strings.TrimSpace(string(data))
	if len(content) == 0 {
		return -1, nil
	}

	// Parse the value (format is "=X" where X is the counter)
	if content[0] == '=' && len(content) > 1 {
		value, err := strconv.Atoi(content[1:])
		if err != nil {
			return -1, err
		}
		return value, nil
	}

	return -1, nil
}
