//go:build !linux

package process

import (
	"errors"
	"os"
)

func retainedHead(file *os.File) (int64, error)          { return 0, nil }
func recordRetainedHead(file *os.File, head int64) error { return errors.ErrUnsupported }

func lockLogFile(file *os.File, exclusive bool) error { return nil }
func unlockLogFile(file *os.File)                     {}

func initializeLogRetention(file *os.File) error { return nil }

func removeLogRetention(file *os.File) {}
