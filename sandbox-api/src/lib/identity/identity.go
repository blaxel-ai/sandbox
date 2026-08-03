// Package identity resolves the unprivileged identity that user workloads run
// as, while the sandbox API itself keeps the privileges it needs (FUSE drive
// mounts, WireGuard, CA bundle, keep-alive, port probing).
//
// The identity is configured with BL_SANDBOX_USER, using Docker's USER syntax:
// "app", "10001", "app:app" or "10001:10001". It only takes effect once
// BL_SANDBOX_USER_ENABLED is on, so the runtime can export the image USER
// before anyone opts in. When either is missing the whole mechanism is
// disabled and everything keeps running as the API user (root), which is the
// historical behaviour. --user opts in by itself.
package identity

import (
	"fmt"
	"os"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/sirupsen/logrus"
)

const (
	// EnvUser is the environment variable holding the workload identity.
	EnvUser = "BL_SANDBOX_USER"
	// EnvEnabled opts into the identity coming from the environment. It
	// defaults to false: the runtime exports the image USER for every
	// sandbox, and honouring it unprompted would change how existing images
	// behave.
	EnvEnabled = "BL_SANDBOX_USER_ENABLED"
)

// Identity is a resolved, unprivileged POSIX identity.
type Identity struct {
	Uid    int
	Gid    int
	Groups []uint32
	Name   string
	Home   string
}

var (
	once     sync.Once
	resolved *Identity
	spec     string
	source   = EnvUser
)

// SetSpec sets the workload identity from the command line, taking precedence
// over the environment. It must be called before the first Get.
func SetSpec(value string) {
	if value = strings.TrimSpace(value); value != "" {
		spec = value
		source = "--user"
	}
}

// Get returns the workload identity, or nil when none is configured. The
// resolution happens once: the value cannot change during the lifetime of the
// process, so no request can influence which user its work runs as.
func Get() *Identity {
	once.Do(func() {
		if spec == "" {
			if !enabled() {
				return
			}
			spec = strings.TrimSpace(os.Getenv(EnvUser))
		}
		if spec == "" {
			return
		}
		id, err := resolve(spec)
		if err != nil {
			// Failing open (running as root) would silently give every
			// workload the privileges this feature exists to remove.
			logrus.WithError(err).Fatalf("Invalid %s=%q", source, spec)
		}
		if id.Uid == 0 {
			logrus.Fatalf("%s=%q resolves to uid 0; the workload identity must be unprivileged", source, spec)
		}
		logrus.WithFields(logrus.Fields{
			"user": id.Name,
			"uid":  id.Uid,
			"gid":  id.Gid,
			"home": id.Home,
		}).Info("Workload identity enabled: processes, terminals and filesystem operations run unprivileged")
		resolved = id
	})
	return resolved
}

// enabled reports whether the environment opts into the workload identity.
// Anything unparseable counts as off, the same as unset.
func enabled() bool {
	on, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(EnvEnabled)))
	return err == nil && on
}

// resolve parses a Docker USER string and looks the parts up in the passwd and
// group databases.
func resolve(spec string) (*Identity, error) {
	userPart, groupPart, hasGroup := strings.Cut(spec, ":")
	if userPart == "" {
		return nil, fmt.Errorf("empty user part")
	}

	id := &Identity{Gid: -1}

	if uid, err := strconv.Atoi(userPart); err == nil {
		id.Uid = uid
		id.Name = userPart
		if u, err := user.LookupId(userPart); err == nil {
			id.Name = u.Username
			id.Home = u.HomeDir
			id.Gid, _ = strconv.Atoi(u.Gid)
		}
	} else {
		u, err := user.Lookup(userPart)
		if err != nil {
			return nil, fmt.Errorf("user %q not found: %w", userPart, err)
		}
		id.Uid, _ = strconv.Atoi(u.Uid)
		id.Gid, _ = strconv.Atoi(u.Gid)
		id.Name = u.Username
		id.Home = u.HomeDir
	}

	if hasGroup && groupPart != "" {
		gid, err := strconv.Atoi(groupPart)
		if err != nil {
			g, err := user.LookupGroup(groupPart)
			if err != nil {
				return nil, fmt.Errorf("group %q not found: %w", groupPart, err)
			}
			gid, _ = strconv.Atoi(g.Gid)
		}
		id.Gid = gid
	}
	if id.Gid < 0 {
		id.Gid = id.Uid
	}
	if id.Home == "" {
		id.Home = "/"
	}

	id.Groups = supplementaryGroups(id.Name, id.Gid)
	return id, nil
}

// supplementaryGroups mirrors initgroups(3): every group the user is a member
// of, plus its primary group.
func supplementaryGroups(name string, gid int) []uint32 {
	groups := []uint32{uint32(gid)}
	if name == "" {
		return groups
	}
	u, err := user.Lookup(name)
	if err != nil {
		return groups
	}
	ids, err := u.GroupIds()
	if err != nil {
		return groups
	}
	for _, raw := range ids {
		g, err := strconv.Atoi(raw)
		if err != nil || g == gid {
			continue
		}
		groups = append(groups, uint32(g))
	}
	return groups
}

// Credential returns the syscall credential to attach to a spawned process, or
// nil when no identity is configured.
func (id *Identity) Credential() *syscall.Credential {
	if id == nil {
		return nil
	}
	return &syscall.Credential{
		Uid:    uint32(id.Uid),
		Gid:    uint32(id.Gid),
		Groups: id.Groups,
	}
}

// DecorateEnv overrides the identity-related variables of an environment slice
// so a spawned process sees a coherent HOME/USER/LOGNAME.
func (id *Identity) DecorateEnv(env []string) []string {
	if id == nil {
		return env
	}
	overrides := map[string]string{
		"HOME":    id.Home,
		"USER":    id.Name,
		"LOGNAME": id.Name,
	}
	out := make([]string, 0, len(env)+len(overrides))
	for _, kv := range env {
		key, _, ok := strings.Cut(kv, "=")
		if ok {
			if _, replaced := overrides[key]; replaced {
				continue
			}
		}
		out = append(out, kv)
	}
	for key, value := range overrides {
		if value != "" {
			out = append(out, key+"="+value)
		}
	}
	return out
}

// Do runs fn with the calling thread's filesystem identity set to the workload
// user, so every path the kernel resolves inside fn is checked against that
// user's permissions instead of root's.
//
// setfsuid(2)/setfsgid(2) are per-thread, hence the locked OS thread; they also
// drop the thread's CAP_DAC_OVERRIDE / CAP_DAC_READ_SEARCH for the duration,
// which is precisely the point: it stops the filesystem API from being used to
// overwrite root-owned files (for example the blfs binary the drive mount runs
// as root) and thereby escalate out of the unprivileged identity.
//
// Limitation: the fsgid change covers the primary group only. Access granted
// exclusively through a supplementary group of the workload user is not
// honoured inside Do.
func (id *Identity) Do(fn func() error) error {
	if id == nil {
		return fn()
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	previousGid := setfsgid(id.Gid)
	previousUid := setfsuid(id.Uid)

	// setfsuid(2)/setfsgid(2) have no error return: they report the previous
	// value whether or not they changed anything. Reading the value back is the
	// only way to know the thread really lost its root filesystem privileges.
	if current := setfsuid(id.Uid); current != id.Uid {
		setfsuid(previousUid)
		setfsgid(previousGid)
		return fmt.Errorf("failed to drop filesystem uid to %d", id.Uid)
	}
	if current := setfsgid(id.Gid); current != id.Gid {
		setfsuid(previousUid)
		setfsgid(previousGid)
		return fmt.Errorf("failed to drop filesystem gid to %d", id.Gid)
	}

	defer func() {
		// Restoring must succeed: a thread left with a non-root fsuid would
		// silently break the privileged work (mounts, tunnel) that runs on it
		// later. Restoring the previous value rather than 0 keeps nested calls
		// correct.
		setfsuid(previousUid)
		setfsgid(previousGid)
		if current := setfsuid(previousUid); current != previousUid {
			logrus.Fatalf("Failed to restore filesystem uid to %d", previousUid)
		}
	}()

	return fn()
}

// Do runs fn under the process-wide workload identity when one is configured.
func Do(fn func() error) error {
	return Get().Do(fn)
}
