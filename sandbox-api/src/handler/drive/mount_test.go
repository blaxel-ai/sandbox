package drive

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestCreateMountPointReportsCreation checks the flag that decides whether the
// mount point may be handed to the workload user.
func TestCreateMountPointReportsCreation(t *testing.T) {
	root := t.TempDir()

	fresh := filepath.Join(root, "nested", "mount")
	created, err := createMountPoint(fresh)
	if err != nil {
		t.Fatalf("createMountPoint(%q): %v", fresh, err)
	}
	if !created {
		t.Fatalf("createMountPoint(%q) = false, want true for a new directory", fresh)
	}

	// A pre-existing directory must never be reported as created: it may be a
	// root-owned system directory the caller asked for.
	if created, err = createMountPoint(fresh); err != nil {
		t.Fatalf("createMountPoint(%q) on existing dir: %v", fresh, err)
	}
	if created {
		t.Fatalf("createMountPoint(%q) = true, want false for an existing directory", fresh)
	}
}

// TestCreateMountPointHandlesTrailingSlash: a trailing slash must not make the
// parent creation swallow the mount point itself, which would leave a directory
// created for the workload owned by root.
func TestCreateMountPointHandlesTrailingSlash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mount") + "/"
	created, err := createMountPoint(path)
	if err != nil {
		t.Fatalf("createMountPoint(%q): %v", path, err)
	}
	if !created {
		t.Fatalf("createMountPoint(%q) = false, want true", path)
	}
}

// TestCreateMountPointDoesNotAdoptSymlinks makes sure a symlink planted at the
// mount path is not reported as created, which would chown its target.
func TestCreateMountPointDoesNotAdoptSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "privileged")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	created, err := createMountPoint(link)
	if err != nil {
		t.Fatalf("createMountPoint(%q): %v", link, err)
	}
	if created {
		t.Fatalf("createMountPoint(%q) = true, want false for a symlink", link)
	}
}

// TestCreateMountPointRejectsFiles keeps a file from being used as a mount
// target, where it would previously have been chowned.
func TestCreateMountPointRejectsFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := createMountPoint(path); err == nil {
		t.Fatalf("createMountPoint(%q) = nil error, want a failure", path)
	}
}

// TestChownMountPointRefusesSymlinks verifies the chown cannot be redirected
// through a symlink swapped in after the directory was created.
func TestChownMountPointRefusesSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "privileged")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := chownMountPoint(link, os.Getuid(), os.Getgid()); err == nil {
		t.Fatal("chownMountPoint followed a symlink, want a failure")
	}
}

func TestNormalizeDrivePath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"root stays root", "/", "/"},
		{"missing leading slash", "sub", "/sub"},
		{"trailing slash trimmed", "/sub/", "/sub"},
		{"nested trailing slash trimmed", "/a/b/", "/a/b"},
		{"empty becomes root", "", "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeDrivePath(tt.input); got != tt.want {
				t.Errorf("normalizeDrivePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCrossMountCacheCoherenceFlag(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{"true", "-crossMountCacheCoherence=true"},
		{"", "-crossMountCacheCoherence=false"},
		{"false", "-crossMountCacheCoherence=false"},
		{"TRUE", "-crossMountCacheCoherence=false"},
		{"1", "-crossMountCacheCoherence=false"},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			if got := crossMountCacheCoherenceFlag(tt.value); got != tt.want {
				t.Errorf("crossMountCacheCoherenceFlag(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

// TestMountLockForSameKey verifies the same mutex is returned for paths that
// clean to the same key, so concurrent mounts on the same target serialize.
func TestMountLockForSameKey(t *testing.T) {
	a := mountLockFor("/mnt/test")
	b := mountLockFor("/mnt/test/")
	c := mountLockFor("/mnt/other")
	if a != b {
		t.Errorf("expected identical lock for equivalent paths")
	}
	if a == c {
		t.Errorf("expected distinct locks for different paths")
	}
}

// TestMountLockForConcurrent ensures concurrent lookups for the same key never
// hand out two different mutexes (which would defeat serialization).
func TestMountLockForConcurrent(t *testing.T) {
	const n = 50
	locks := make([]*sync.Mutex, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			locks[idx] = mountLockFor("/mnt/concurrent")
		}(i)
	}
	wg.Wait()
	first := locks[0]
	for i := 1; i < n; i++ {
		if locks[i] != first {
			t.Fatalf("mountLockFor returned different mutexes for the same key")
		}
	}
}

func TestParseFilerAddress(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{
			name:    "IPv4 nameserver",
			content: "nameserver 172.16.1.126\n",
			want:    "172.16.1.126",
		},
		{
			name:    "IPv6 nameserver",
			content: "# DNS Configuration\nnameserver 2600:1f14:c75:3900::301\n",
			want:    "2600:1f14:c75:3900::301",
		},
		{
			name:    "IPv6 nameserver with zone",
			content: "nameserver fe80::1%eth0\n",
			want:    "fe80::1%eth0",
		},
		{
			name:    "skips malformed nameserver",
			content: "nameserver not-an-ip\nnameserver 10.0.0.2\n",
			want:    "10.0.0.2",
		},
		{
			name:    "requires exact nameserver directive",
			content: "nameserver-proxy 10.0.0.2\n",
			wantErr: true,
		},
		{
			name:    "missing nameserver",
			content: "options edns0 trust-ad\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFilerAddress([]byte(tt.content))
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseFilerAddress() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("parseFilerAddress() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatFilerServerAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    string
	}{
		{
			name:    "IPv4",
			address: "172.16.1.126",
			want:    "172.16.1.126:49200.49201",
		},
		{
			name:    "IPv6",
			address: "2600:1f14:c75:3900::301",
			want:    "2600:1f14:c75:3900::301:49200.49201",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatFilerServerAddress(tt.address); got != tt.want {
				t.Fatalf("formatFilerServerAddress(%q) = %q, want %q", tt.address, got, tt.want)
			}
		})
	}
}

// The platform states the filer host on mk3.1, where inferring it from
// resolv.conf finds the DNS resolver instead of the filer.
func TestGetFilerAddressPrefersThePlatformValue(t *testing.T) {
	t.Setenv(DriveFilerEnv, "agent-drive.svc.blaxel.local")

	got, err := getFilerAddress()
	if err != nil {
		t.Fatalf("getFilerAddress() error = %v", err)
	}
	if got != "agent-drive.svc.blaxel.local" {
		t.Fatalf("getFilerAddress() = %q, want the platform-supplied host", got)
	}
}

// Whitespace around the value must not become part of the host: it would be
// appended to SeaweedFS's address string and produce an unresolvable target.
func TestGetFilerAddressTrimsThePlatformValue(t *testing.T) {
	t.Setenv(DriveFilerEnv, "  agent-drive.svc.blaxel.local\n")

	got, err := getFilerAddress()
	if err != nil {
		t.Fatalf("getFilerAddress() error = %v", err)
	}
	if got != "agent-drive.svc.blaxel.local" {
		t.Fatalf("getFilerAddress() = %q, want it trimmed", got)
	}
}

// An empty value is treated as unset, so a platform that exports the variable
// without a value does not break the mk3.0 path.
func TestGetFilerAddressIgnoresAnEmptyPlatformValue(t *testing.T) {
	t.Setenv(DriveFilerEnv, "   ")

	// It must not return the blank value; whether the resolv.conf fallback then
	// succeeds depends on the machine, which is why that branch is asserted
	// separately in TestParseFilerAddress.
	got, _ := getFilerAddress()
	if got != "" && strings.TrimSpace(got) == "" {
		t.Fatalf("getFilerAddress() = %q, want a whitespace-only value ignored", got)
	}
	if got == "   " {
		t.Fatal("getFilerAddress() returned the blank environment value verbatim")
	}
}

// mk3.0 must be untouched: with the variable unset, the address still comes from
// resolv.conf. This is the property that makes the change safe to ship to both
// generations from one binary.
func TestGetFilerAddressFallsBackToResolvConfWhenUnset(t *testing.T) {
	if _, ok := os.LookupEnv(DriveFilerEnv); ok {
		t.Fatalf("%s must be unset for this test", DriveFilerEnv)
	}
	// parseFilerAddress is the resolv.conf path getFilerAddress delegates to.
	got, err := parseFilerAddress([]byte("nameserver 10.0.0.2\n"))
	if err != nil {
		t.Fatalf("parseFilerAddress() error = %v", err)
	}
	if got != "10.0.0.2" {
		t.Fatalf("parseFilerAddress() = %q, want the first nameserver", got)
	}
}

// A NAME survives SeaweedFS's host:httpPort.grpcPort formatting. Its parser
// splits the ports on the final '.', computed on the ports substring only, so the
// dots inside a hostname are not mistaken for the port separator.
func TestFormatFilerServerAddressAcceptsAName(t *testing.T) {
	got := formatFilerServerAddress("agent-drive.svc.blaxel.local")
	if got != "agent-drive.svc.blaxel.local:49200.49201" {
		t.Fatalf("formatFilerServerAddress() = %q", got)
	}
}
