package identity

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
)

func currentUser(t *testing.T) *user.User {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current: %v", err)
	}
	return u
}

func TestResolve(t *testing.T) {
	u := currentUser(t)
	gid, _ := strconv.Atoi(u.Gid)
	uid, _ := strconv.Atoi(u.Uid)

	for _, spec := range []string{u.Username, u.Uid, u.Username + ":" + u.Gid, u.Uid + ":" + u.Gid} {
		id, err := resolve(spec)
		if err != nil {
			t.Fatalf("resolve(%q): %v", spec, err)
		}
		if id.Uid != uid || id.Gid != gid {
			t.Fatalf("resolve(%q) = uid %d gid %d, want uid %d gid %d", spec, id.Uid, id.Gid, uid, gid)
		}
		if !slices.Contains(id.Groups, uint32(gid)) {
			t.Fatalf("resolve(%q) groups %v missing primary gid %d", spec, id.Groups, gid)
		}
	}
}

func TestResolveRejectsUnknown(t *testing.T) {
	for _, spec := range []string{"", "no-such-user-9d1f", currentUser(t).Username + ":no-such-group-9d1f"} {
		if _, err := resolve(spec); err == nil {
			t.Fatalf("resolve(%q) succeeded, want error", spec)
		}
	}
}

func TestEnabled(t *testing.T) {
	for value, want := range map[string]bool{"": false, "false": false, "0": false, "maybe": false, "true": true, "1": true, " true ": true} {
		t.Setenv(EnvEnabled, value)
		if got := enabled(); got != want {
			t.Fatalf("enabled() with %s=%q = %v, want %v", EnvEnabled, value, got, want)
		}
	}
}

func TestDecorateEnv(t *testing.T) {
	id := &Identity{Uid: 10001, Gid: 10001, Name: "app", Home: "/home/app"}
	env := id.DecorateEnv([]string{"HOME=/root", "USER=root", "LOGNAME=root", "PATH=/usr/bin"})

	want := map[string]bool{"HOME=/home/app": false, "USER=app": false, "LOGNAME=app": false, "PATH=/usr/bin": false}
	for _, kv := range env {
		if _, ok := want[kv]; !ok {
			t.Fatalf("unexpected entry %q in %v", kv, env)
		}
		want[kv] = true
	}
	for kv, seen := range want {
		if !seen {
			t.Fatalf("missing entry %q in %v", kv, env)
		}
	}
}

func TestDecorateEnvDisabled(t *testing.T) {
	var id *Identity
	env := []string{"HOME=/root"}
	if got := id.DecorateEnv(env); len(got) != 1 || got[0] != "HOME=/root" {
		t.Fatalf("DecorateEnv on nil identity = %v, want unchanged", got)
	}
}

func TestCredentialDisabled(t *testing.T) {
	var id *Identity
	if got := id.Credential(); got != nil {
		t.Fatalf("Credential on nil identity = %v, want nil", got)
	}
}

// TestDoDropsRootAccess is the actual guarantee: inside Do, a root-owned 0600
// file must be unreadable, and outside it must be readable again.
func TestDoDropsRootAccess(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}

	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("root only"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	id := &Identity{Uid: 65534, Gid: 65534, Name: "nobody", Home: "/"}

	err := id.Do(func() error {
		if _, err := os.ReadFile(secret); err == nil {
			t.Error("root-owned 0600 file was readable under the workload identity")
		}
		// Nested calls must not restore root access early.
		return id.Do(func() error {
			if _, err := os.ReadFile(secret); err == nil {
				t.Error("root-owned 0600 file was readable inside a nested Do")
			}
			return nil
		})
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}

	if _, err := os.ReadFile(secret); err != nil {
		t.Fatalf("root access was not restored after Do: %v", err)
	}
}

// reset undoes the package-level memoisation so a test can exercise Get with a
// fresh configuration.
func reset(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		once = sync.Once{}
		resolved = nil
		spec = ""
		source = EnvUser
	})
	once = sync.Once{}
	resolved = nil
	spec = ""
	source = EnvUser
}

func TestGetIgnoresEnvironmentWhenDisabled(t *testing.T) {
	for _, enabledValue := range []string{"", "false", "0", "not-a-bool"} {
		reset(t)
		t.Setenv(EnvUser, currentUser(t).Username)
		t.Setenv(EnvEnabled, enabledValue)

		if id := Get(); id != nil {
			t.Fatalf("Get() with %s=%q = %+v, want nil", EnvEnabled, enabledValue, id)
		}
	}
}

func TestGetUsesEnvironmentWhenEnabled(t *testing.T) {
	u := currentUser(t)
	if u.Uid == "0" {
		t.Skip("requires a non-root test user")
	}
	reset(t)
	t.Setenv(EnvUser, u.Username)
	t.Setenv(EnvEnabled, "true")

	id := Get()
	if id == nil {
		t.Fatal("Get() = nil, want the environment identity")
	}
	if id.Name != u.Username {
		t.Fatalf("Get().Name = %q, want %q", id.Name, u.Username)
	}
}

// SetSpec is the --user flag: it is the explicit opt-in, so it must not need
// the environment gate.
func TestSetSpecBypassesTheEnvironmentGate(t *testing.T) {
	u := currentUser(t)
	if u.Uid == "0" {
		t.Skip("requires a non-root test user")
	}
	reset(t)
	t.Setenv(EnvEnabled, "false")
	SetSpec(" " + u.Username + " ")

	id := Get()
	if id == nil {
		t.Fatal("Get() after SetSpec = nil, want the flag identity")
	}
	if id.Name != u.Username {
		t.Fatalf("Get().Name = %q, want %q", id.Name, u.Username)
	}
	if source != "--user" {
		t.Fatalf("source = %q, want %q", source, "--user")
	}
}

func TestSetSpecIgnoresEmptyValues(t *testing.T) {
	reset(t)
	SetSpec("   ")
	if spec != "" {
		t.Fatalf("spec = %q, want empty", spec)
	}
}

// Nothing but a configured identity may make the API act as a different user:
// with no identity, Do must run fn as-is rather than fail or drop privileges.
func TestDoWithoutIdentityRunsAsIs(t *testing.T) {
	reset(t)
	ran := false
	if err := Do(func() error {
		ran = true
		return nil
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !ran {
		t.Fatal("Do did not run fn")
	}
}

func TestDoPropagatesErrors(t *testing.T) {
	want := errors.New("boom")
	id := &Identity{Uid: 65534, Gid: 65534, Name: "nobody", Home: "/"}
	if os.Geteuid() != 0 {
		id = nil
	}
	if got := id.Do(func() error { return want }); !errors.Is(got, want) {
		t.Fatalf("Do error = %v, want %v", got, want)
	}
}
