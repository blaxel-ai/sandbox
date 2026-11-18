package handler

import (
	"errors"

	"github.com/blaxel-ai/sandbox-api/src/handler/process"
)

// codeProcessWorkingDirUnusable marks an exec refused because its workingDir
// cannot be used: the path exists and is not a directory, a parent is a file,
// or the user the command runs as may not create it. A workingDir that is
// only missing is created instead, like `docker run -w`. The directory is
// checked before the process starts, so the command did not run, and the same
// request fails the same way until the path changes.
const codeProcessWorkingDirUnusable = "PROCESS_WORKING_DIR_UNUSABLE"

// workingDirErrorCode is codeProcessWorkingDirUnusable for an exec refused
// because of its working directory, and "" for any other error.
func workingDirErrorCode(err error) string {
	var wdErr *process.WorkingDirError
	if errors.As(err, &wdErr) {
		return codeProcessWorkingDirUnusable
	}
	return ""
}
