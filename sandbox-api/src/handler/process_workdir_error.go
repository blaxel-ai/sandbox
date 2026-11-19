package handler

import (
	"errors"
	"fmt"
	"syscall"

	"github.com/blaxel-ai/sandbox-api/src/handler/process"
)

// workingDirFailure is an exec refused because its workingDir cannot be used:
// the path exists and is not a directory, a parent is a file, or the user the
// command runs as may not create it. A workingDir that is only missing is
// created instead, like `docker run -w`. The directory is checked before the
// process starts, so the command did not run, and the same request fails the
// same way until the path changes (PROCESS_WORKING_DIR_UNUSABLE).
type workingDirFailure struct {
	msg string
	err error
}

func (e *workingDirFailure) Error() string { return e.msg }
func (e *workingDirFailure) Unwrap() error { return e.err }

// workingDirError is err with a message that keeps the original error and adds
// that the command did not run and how to fix the path for the cause at hand,
// or nil when err is not about the working directory.
func workingDirError(err error) error {
	var wdErr *process.WorkingDirError
	if !errors.As(err, &wdErr) {
		return nil
	}
	var next string
	switch {
	case errors.Is(wdErr, syscall.ENOTDIR):
		next = "Set workingDir to a directory, or to a path whose existing parents are all directories, then resend."
	case errors.Is(wdErr, syscall.EACCES), errors.Is(wdErr, syscall.EPERM), errors.Is(wdErr, syscall.EROFS):
		next = "Use a workingDir the command's user can create, such as one under its home directory or /tmp, or create this one in the image owned by that user, then resend."
	default:
		next = "Fix the path or use another workingDir, then resend."
	}
	return &workingDirFailure{
		msg: fmt.Sprintf("%s. The command did not run, and resending it unchanged fails the same way. %s", err, next),
		err: err,
	}
}
