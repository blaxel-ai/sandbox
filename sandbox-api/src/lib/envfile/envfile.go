// Package envfile loads the part of the environment a guest cannot receive on
// its kernel command line.
//
// The command line is bounded, so the runtime moves everything that does not
// fit into a file mounted from a secret and names it in BL_ENV_VAR_PATH. The
// guest's init (the metamorph wrapper) reads that file before exec'ing the
// workload, so in the normal case the API already has those variables in its
// own environment and there is nothing to do here.
//
// It is not the case for every image: the wrapper baked into an image predates
// the file for images converted before it existed, and an init that cannot read
// the mount (or is not the wrapper at all) drops the whole user environment
// silently. Reading the file here makes the API the authority on its own
// environment instead of the image's init, which is what every process,
// terminal and MCP tool it spawns inherits.
package envfile

import (
	"bytes"
	"fmt"
	"os"
)

// PathVar names the file holding the overflowed environment.
const PathVar = "BL_ENV_VAR_PATH"

// Load sets every variable of the file named by PathVar that the process does
// not already have, and reports how many it set. It is a no-op when the
// variable is unset, which is the case whenever the whole environment fit on
// the command line.
//
// Variables already present are left alone: the command line carries the
// platform's own variables and is the more authoritative of the two, and a
// variable the wrapper already loaded holds the same value anyway.
func Load() (int, error) {
	path := os.Getenv(PathVar)
	if path == "" {
		return 0, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read %s=%q: %w", PathVar, path, err)
	}

	loaded := 0
	for name, value := range parse(content) {
		if _, exists := os.LookupEnv(name); exists {
			continue
		}
		if err := os.Setenv(name, value); err != nil {
			return loaded, fmt.Errorf("set %q from %q: %w", name, path, err)
		}
		loaded++
	}
	return loaded, nil
}

// parse reads NUL-delimited KEY\0VALUE\0 pairs, the format the runtime writes:
// a name cannot contain a NUL, so values carrying newlines or '=' stay
// unambiguous. A trailing name with no value is incomplete and dropped, as is a
// nameless pair. A repeated name keeps its last value, as setenv(3) would.
func parse(content []byte) map[string]string {
	env := map[string]string{}
	fields := bytes.Split(content, []byte{0})
	for i := 0; i+1 < len(fields); i += 2 {
		if name := string(fields[i]); name != "" {
			env[name] = string(fields[i+1])
		}
	}
	return env
}
