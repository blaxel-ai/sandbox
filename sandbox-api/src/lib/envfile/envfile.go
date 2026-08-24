// Package envfile loads the part of the environment a guest cannot receive on
// its kernel command line.
//
// The command line is bounded, so the runtime moves everything that does not
// fit into a file mounted from a secret and names it in BL_ENV_VAR_PATH. The
// guest's init (the metamorph wrapper) reads that file before exec'ing the
// workload, but may have loaded an older version before a restart.
//
// Reading the file here makes the API authoritative for the variables it
// carries. This keeps a restarted API in sync with the file the host rewrote,
// and ensures every process, terminal and MCP tool it spawns inherits them.
package envfile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
)

// PathVar names the file holding the overflowed environment.
const PathVar = "BL_ENV_VAR_PATH"

// Load sets every variable carried by the file named by PathVar, overwriting
// inherited values, and reports how many it applied. It is a no-op when the
// variable is unset, which is the case whenever the whole environment fit on
// the command line.
//
// PathVar itself is never applied from the file, so the file cannot redirect a
// subsequent read.
//
// A variable that cannot be applied costs only itself: the rest of the
// environment is loaded and the failures are returned together, because losing
// the whole environment over one unusable name is the very failure this exists
// to prevent.
func Load() (int, error) {
	path := os.Getenv(PathVar)
	if path == "" {
		return 0, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read %s=%q: %w", PathVar, path, err)
	}

	env := parse(content)
	if len(env) == 0 {
		if len(content) == 0 {
			return 0, nil
		}
		// Content that yields no pair is a format the runtime and this parser
		// disagree on. Silence here would look exactly like the environment
		// loss this package exists to fix.
		return 0, fmt.Errorf(`%s=%q holds %d bytes but no KEY\0VALUE\0 pair: unexpected format`, PathVar, path, len(content))
	}

	loaded := 0
	var failures []error
	for name, value := range env {
		if name == PathVar {
			continue
		}
		if err := os.Setenv(name, value); err != nil {
			failures = append(failures, fmt.Errorf("set %q: %w", name, err))
			continue
		}
		loaded++
	}
	if len(failures) > 0 {
		return loaded, fmt.Errorf("%s=%q: %w", PathVar, path, errors.Join(failures...))
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
