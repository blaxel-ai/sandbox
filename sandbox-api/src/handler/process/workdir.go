package process

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/blaxel-ai/sandbox-api/src/lib/identity"
)

// workingDirMode is what a created working directory gets before the umask:
// the mode Docker gives the directory it creates for `docker run -w`.
const workingDirMode = 0o755

// prepareWorkingDir makes dir usable as the working directory of a new
// process. A directory that does not exist yet is created with its missing
// parents, like `mkdir -p`, which is what `docker run -w`, a compose
// `working_dir` and a Kubernetes `workingDir` do. Harnesses written against
// those (Harbor runs its own setup `mkdir -p <workdir>` with that very workdir
// as cwd) otherwise cannot start at all on an image without the directory.
//
// Only the creation runs under the workload identity, so the permission checks
// are the exec user's and what gets created belongs to that user. Docker
// creates the directory as root even for a non-root USER, which leaves the
// process in a working directory it cannot write to; creating it as the user
// avoids that, and it cannot reach further than the user's own `mkdir -p`
// could. Without a configured identity everything runs as root, exactly like
// Docker.
//
// An existing path is looked at with the API's own privileges, as it always
// was, and access to it is left to the child. identity.Do switches the
// filesystem uid and primary gid only, while the child also gets the user's
// supplementary groups: checking access under Do would reject directories the
// process can in fact use through one of those groups. Creation has that
// limitation (a parent writable only through a supplementary group refuses
// the mkdir), the same as every other filesystem operation done for the user.
//
// What is not a missing directory stays an error: a path that exists but is
// not a directory, a parent that is a file, a directory the user may not
// create.
func prepareWorkingDir(dir string) error {
	return prepareWorkingDirAs(identity.Get(), dir)
}

// prepareWorkingDirAs is prepareWorkingDir for an explicit identity; nil means
// the API's own user.
func prepareWorkingDirAs(id *identity.Identity, dir string) error {
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		err = id.Do(func() error {
			if err := os.MkdirAll(dir, workingDirMode); err != nil {
				return workingDirError(dir, err)
			}
			return nil
		})
		if err != nil {
			return err
		}
		info, err = os.Stat(dir)
	}
	if err != nil {
		return workingDirError(dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("working directory '%s' is not a directory", dir)
	}
	return nil
}

func workingDirError(dir string, err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		// Name the operation and the component that failed, which is not
		// always dir itself: mkdir fails on the first parent it cannot create.
		return fmt.Errorf("cannot use working directory '%s': %s %s: %w", dir, pathErr.Op, pathErr.Path, pathErr.Err)
	}
	return fmt.Errorf("cannot use working directory '%s': %w", dir, err)
}
