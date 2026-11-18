package terminal

import (
	"slices"
	"testing"
)

func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				m[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return m
}

func TestTerminalEnvReplacesInheritedTerm(t *testing.T) {
	// sandbox-api is often started with TERM=linux; the PTY is xterm.js.
	env := envMap(terminalEnv([]string{"TERM=linux", "PATH=/bin"}, nil, true))
	if env["TERM"] != "xterm-256color" {
		t.Fatalf("TERM = %q, want xterm-256color", env["TERM"])
	}
	if env["COLORTERM"] != "truecolor" {
		t.Fatalf("COLORTERM = %q, want truecolor", env["COLORTERM"])
	}
	if env["PATH"] != "/bin" {
		t.Fatalf("PATH = %q, want the inherited /bin", env["PATH"])
	}
}

func TestTerminalEnvKeepsCallerTerm(t *testing.T) {
	env := envMap(terminalEnv([]string{"TERM=linux"}, map[string]string{"TERM": "screen"}, true))
	if env["TERM"] != "screen" {
		t.Fatalf("TERM = %q, want the caller's screen", env["TERM"])
	}
}

func TestTerminalEnvLocale(t *testing.T) {
	cases := []struct {
		name       string
		system     []string
		overrides  map[string]string
		utf8Locale bool
		wantLang   string
	}{
		{"no locale set, UTF-8 available", nil, nil, true, "C.UTF-8"},
		{"no locale set, UTF-8 unavailable", nil, nil, false, ""},
		{"inherited LANG wins", []string{"LANG=fr_FR.UTF-8"}, nil, true, "fr_FR.UTF-8"},
		{"inherited LC_ALL wins", []string{"LC_ALL=en_US.UTF-8"}, nil, true, ""},
		{"caller LC_CTYPE wins", nil, map[string]string{"LC_CTYPE": "en_US.UTF-8"}, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := envMap(terminalEnv(tc.system, tc.overrides, tc.utf8Locale))
			if env["LANG"] != tc.wantLang {
				t.Fatalf("LANG = %q, want %q", env["LANG"], tc.wantLang)
			}
		})
	}
}

func TestTerminalEnvHasNoDuplicateKeys(t *testing.T) {
	out := terminalEnv(
		[]string{"TERM=linux", "HOME=/root", "LANG=C"},
		map[string]string{"HOME": "/blaxel", "LANG": "C.UTF-8"},
		true,
	)
	var keys []string
	for k := range envMap(out) {
		keys = append(keys, k)
	}
	if len(keys) != len(out) {
		slices.Sort(out)
		t.Fatalf("duplicate keys in %v", out)
	}
	if env := envMap(out); env["HOME"] != "/blaxel" || env["LANG"] != "C.UTF-8" {
		t.Fatalf("overrides not applied: %v", out)
	}
}
