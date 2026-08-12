package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

// write puts content in a temporary file and points PathVar at it.
func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(PathVar, path)
	return path
}

// absent guarantees the name is unset for the duration of the test whatever the
// ambient environment holds, and restores it afterwards: a variable the test
// expects Load to set must not be one the process already has, or Load skips it
// and the test measures nothing.
func absent(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		t.Setenv(name, "") // records the original value for restoration
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadSetsTheMissingVariables(t *testing.T) {
	absent(t, "TEST_ENV_VAR_001", "PORT")
	write(t, "TEST_ENV_VAR_001\x00value_001\x00PORT\x0080\x00")

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded != 2 {
		t.Errorf("Load() = %d, want 2", loaded)
	}
	if got := os.Getenv("TEST_ENV_VAR_001"); got != "value_001" {
		t.Errorf("TEST_ENV_VAR_001 = %q, want %q", got, "value_001")
	}
	if got := os.Getenv("PORT"); got != "80" {
		t.Errorf("PORT = %q, want %q", got, "80")
	}
}

// One unusable name must not cost the rest of the environment: the whole point
// of the package is that the user does not silently lose it.
func TestLoadAppliesTheRestDespiteAnUnusableName(t *testing.T) {
	absent(t, "TEST_ENV_VAR_GOOD")
	write(t, "BAD=NAME\x00nope\x00TEST_ENV_VAR_GOOD\x00value\x00")

	loaded, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want the unusable name reported")
	}
	if loaded != 1 {
		t.Errorf("Load() = %d, want 1", loaded)
	}
	if got := os.Getenv("TEST_ENV_VAR_GOOD"); got != "value" {
		t.Errorf("TEST_ENV_VAR_GOOD = %q, want %q", got, "value")
	}
}

// Content this parser finds no pair in means the runtime writes another format,
// which would otherwise look exactly like the loss this package fixes.
func TestLoadReportsAnUnexpectedFormat(t *testing.T) {
	write(t, "KEY=value\nOTHER=value\n")

	loaded, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want an error for content holding no pair")
	}
	if loaded != 0 {
		t.Errorf("Load() = %d, want 0", loaded)
	}
}

// The command line is the authoritative half of the environment, and a variable
// the image's init already loaded holds the same value anyway.
func TestLoadKeepsTheVariablesAlreadySet(t *testing.T) {
	write(t, "ALREADY_SET\x00from_file\x00EMPTY_BUT_SET\x00from_file\x00")
	t.Setenv("ALREADY_SET", "from_command_line")
	t.Setenv("EMPTY_BUT_SET", "")

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded != 0 {
		t.Errorf("Load() = %d, want 0", loaded)
	}
	if got := os.Getenv("ALREADY_SET"); got != "from_command_line" {
		t.Errorf("ALREADY_SET = %q, want %q", got, "from_command_line")
	}
	if got := os.Getenv("EMPTY_BUT_SET"); got != "" {
		t.Errorf("EMPTY_BUT_SET = %q, want empty", got)
	}
}

// Every environment that fits on the kernel command line comes with no file at
// all, which is not an error.
func TestLoadWithoutAPathDoesNothing(t *testing.T) {
	t.Setenv(PathVar, "")

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded != 0 {
		t.Errorf("Load() = %d, want 0", loaded)
	}
}

// A path that cannot be read is the failure this exists to surface: the mount
// is missing and the user environment is gone.
func TestLoadReportsAnUnreadableFile(t *testing.T) {
	t.Setenv(PathVar, filepath.Join(t.TempDir(), "absent"))

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want an error for a missing file")
	}
}

// An empty file is an environment with nothing in it, not a format mismatch.
func TestLoadEmptyFile(t *testing.T) {
	write(t, "")

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded != 0 {
		t.Errorf("Load() = %d, want 0", loaded)
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    map[string]string
	}{
		{
			name:    "values may carry newlines and equals signs",
			content: "KEY\x00a=b\nc\x00",
			want:    map[string]string{"KEY": "a=b\nc"},
		},
		{
			name:    "an empty value is a value",
			content: "KEY\x00\x00OTHER\x00v\x00",
			want:    map[string]string{"KEY": "", "OTHER": "v"},
		},
		{
			name:    "a name with no value is dropped",
			content: "KEY\x00v\x00TRUNCATED",
			want:    map[string]string{"KEY": "v"},
		},
		{
			name:    "a nameless pair is dropped",
			content: "\x00orphan\x00KEY\x00v\x00",
			want:    map[string]string{"KEY": "v"},
		},
		{
			name:    "the last value of a repeated name wins",
			content: "KEY\x00first\x00KEY\x00second\x00",
			want:    map[string]string{"KEY": "second"},
		},
		{
			name:    "a file with no trailing separator still parses",
			content: "KEY\x00v",
			want:    map[string]string{"KEY": "v"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := parse([]byte(testCase.content))
			if len(got) != len(testCase.want) {
				t.Fatalf("parse() = %v, want %v", got, testCase.want)
			}
			for name, value := range testCase.want {
				if got[name] != value {
					t.Errorf("parse()[%q] = %q, want %q", name, got[name], value)
				}
			}
		})
	}
}
