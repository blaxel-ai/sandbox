//go:build !linux

package drive

import (
	"errors"
	"os"
)

var errMountUnsupported = errors.New("drive mounts are only supported on linux")

func openMountTarget(string) (*os.File, error) { return nil, errMountUnsupported }

func checkMountResources(string, uint64) error { return errMountUnsupported }
