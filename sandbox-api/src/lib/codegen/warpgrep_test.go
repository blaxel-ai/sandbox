package codegen

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseFinishArguments(t *testing.T) {
	cases := []struct {
		name      string
		arguments string
		want      []WarpGrepFile
		wantRaw   string
	}{
		{
			name:      "empty",
			arguments: `{"files": ""}`,
			want:      nil,
			wantRaw:   "",
		},
		{
			name:      "single path no lines",
			arguments: `{"files": "src/auth.py"}`,
			want:      []WarpGrepFile{{Path: "src/auth.py"}},
			wantRaw:   "src/auth.py",
		},
		{
			name:      "path with line range",
			arguments: `{"files": "src/auth.py:1-50"}`,
			want:      []WarpGrepFile{{Path: "src/auth.py", Lines: "1-50"}},
			wantRaw:   "src/auth.py:1-50",
		},
		{
			name:      "multiple paths mixed",
			arguments: `{"files": "src/auth.py:1-50\nsrc/user.py"}`,
			want: []WarpGrepFile{
				{Path: "src/auth.py", Lines: "1-50"},
				{Path: "src/user.py"},
			},
			wantRaw: "src/auth.py:1-50\nsrc/user.py",
		},
		{
			name:      "path with colon in name not a line range",
			arguments: `{"files": "C:/Users/foo/bar.py"}`,
			want:      []WarpGrepFile{{Path: "C:/Users/foo/bar.py"}},
			wantRaw:   "C:/Users/foo/bar.py",
		},
		{
			name:      "comma-separated line ranges",
			arguments: `{"files": "src/auth.py:1-20,45-80"}`,
			want:      []WarpGrepFile{{Path: "src/auth.py", Lines: "1-20,45-80"}},
			wantRaw:   "src/auth.py:1-20,45-80",
		},
		{
			name:      "invalid json yields empty",
			arguments: `not json`,
			want:      nil,
			wantRaw:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, raw := parseFinishArguments(tc.arguments)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
			if raw != tc.wantRaw {
				t.Errorf("got raw %q, want %q", raw, tc.wantRaw)
			}
		})
	}
}

func TestLooksLikeLineRange(t *testing.T) {
	cases := map[string]bool{
		"":            false,
		"1":           true,
		"1-50":        true,
		"1-20,45-80":  true,
		"abc":         false,
		"src/foo.py":  false,
		"1.5":         false,
		"1-50 ":       true,
		"1, 2, 3":     true,
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			got := looksLikeLineRange(in)
			if got != want {
				t.Errorf("looksLikeLineRange(%q)=%v want %v", in, got, want)
			}
		})
	}
}

func TestResolveInsideRoot(t *testing.T) {
	dir := t.TempDir()
	abs, _ := filepath.Abs(dir)

	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"empty", "", abs, true},
		{"root itself", abs, abs, true},
		{"relative inside", "subdir", filepath.Join(abs, "subdir"), true},
		{"absolute inside", filepath.Join(abs, "deep", "path"), filepath.Join(abs, "deep", "path"), true},
		{"escape via dotdot", "../etc/passwd", "", false},
		{"absolute outside", "/etc/passwd", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resolveInsideRoot(abs, tc.in)
			if ok != tc.ok {
				t.Fatalf("ok=%v want %v (got=%q)", ok, tc.ok, got)
			}
			if tc.ok && got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestParseLineRanges(t *testing.T) {
	cases := []struct {
		spec  string
		total int
		want  []int
	}{
		{"1-3", 5, []int{0, 1, 2}},
		{"1-3,5", 5, []int{0, 1, 2, 4}},
		{"42", 5, nil},
		{"3-2", 5, nil},
		{"1-100", 3, []int{0, 1, 2}},
		{"", 5, nil},
	}
	for _, tc := range cases {
		got := parseLineRanges(tc.spec, tc.total)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parseLineRanges(%q, %d) = %v want %v", tc.spec, tc.total, got, tc.want)
		}
	}
}

func TestBuildRepoStructure(t *testing.T) {
	dir := t.TempDir()
	// We just verify the function works on a directory and includes the root
	// path as the first entry.
	got := buildRepoStructure(dir, 2, 100)
	if got == "" {
		t.Fatalf("expected non-empty structure")
	}
	if first := splitFirstLine(got); first != dir {
		t.Errorf("expected first line to be repo root %q, got %q", dir, first)
	}
}

func splitFirstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
