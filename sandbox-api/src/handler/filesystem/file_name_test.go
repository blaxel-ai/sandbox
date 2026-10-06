package filesystem

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileJSONCarriesBaseName(t *testing.T) {
	tempDir, fs, cleanup := setupTestEnvironment(t)
	defer cleanup()
	if err := os.MkdirAll(filepath.Join(tempDir, "e2e-fs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "e2e-fs", "hello.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	withContent, err := fs.ReadFile("e2e-fs/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	info, err := fs.GetFileInfo("e2e-fs/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	for label, value := range map[string]any{"FileWithContent": withContent, "File": info} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var decoded File
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Name != "hello.txt" {
			t.Errorf("%s name = %q, want hello.txt (%s)", label, decoded.Name, raw)
		}
	}
}
