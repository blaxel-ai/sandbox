package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blaxel-ai/sandbox-api/src/handler/filesystem"
)

func writeTreeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range map[string]string{
		"a.json":                  `{"a":1}`,
		"notes.md":                "notes",
		"nested/b.json":           `{"b":2}`,
		"nested/deeper/c.json":    `{"c":3}`,
		"node_modules/skip.json":  "skipped",
		".hidden/secret.json":     "hidden",
		"nested/.dot.json":        "dot",
		"nested/deeper/empty.txt": "",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func getTree(t *testing.T, root, query string) (int, filesystem.Directory, string) {
	t.Helper()
	router := newFilesystemTestRouter(t)
	target := "/filesystem/tree" + root
	if query != "" {
		target += "?" + query
	}
	response := serveFilesystem(router, http.MethodGet, target)
	var dir filesystem.Directory
	if response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &dir); err != nil {
			t.Fatal(err)
		}
	}
	return response.Code, dir, response.Body.String()
}

func treeContents(t *testing.T, dir filesystem.Directory) map[string]string {
	t.Helper()
	contents := map[string]string{}
	for _, file := range dir.Files {
		rel := strings.TrimPrefix(file.Path, dir.Path+"/")
		if file.Content == nil {
			contents[rel] = "<none>"
			continue
		}
		contents[rel] = *file.Content
	}
	return contents
}

func TestTreeWithoutOptionsListsDirectChildren(t *testing.T) {
	root := writeTreeFixture(t)
	code, dir, body := getTree(t, root, "")
	if code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", code, body)
	}
	if strings.Contains(body, `"content"`) || strings.Contains(body, `"recursive"`) {
		t.Fatalf("plain tree read returned content or recursive: %s", body)
	}
	if got := treeContents(t, dir); len(got) != 2 || got["a.json"] != "<none>" || got["notes.md"] != "<none>" {
		t.Fatalf("files = %v", got)
	}
	if len(dir.Subdirectories) != 3 {
		t.Fatalf("subdirectories = %d, want 3", len(dir.Subdirectories))
	}
}

func TestTreeRecursiveWithContentAndFilters(t *testing.T) {
	root := writeTreeFixture(t)
	code, dir, body := getTree(t, root, "recursive=true&content=true&patterns=*.json,*.txt&excludeDirs=node_modules&excludeHidden=true")
	if code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", code, body)
	}
	if !dir.Recursive {
		t.Fatalf("recursive tree read did not report recursive: %s", body)
	}
	want := map[string]string{
		"a.json":                  `{"a":1}`,
		"nested/b.json":           `{"b":2}`,
		"nested/deeper/c.json":    `{"c":3}`,
		"nested/deeper/empty.txt": "",
	}
	got := treeContents(t, dir)
	if len(got) != len(want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	for path, content := range want {
		if got[path] != content {
			t.Fatalf("%s = %q, want %q (all: %v)", path, got[path], content, got)
		}
	}
	var subdirs []string
	for _, sub := range dir.Subdirectories {
		subdirs = append(subdirs, strings.TrimPrefix(sub.Path, root+"/"))
	}
	if strings.Join(subdirs, ",") != "nested,nested/deeper" {
		t.Fatalf("subdirectories = %v", subdirs)
	}
}

func TestTreeRecursiveIncludesEverythingByDefault(t *testing.T) {
	root := writeTreeFixture(t)
	code, dir, body := getTree(t, root, "recursive=true")
	if code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", code, body)
	}
	if len(dir.Files) != 8 {
		t.Fatalf("files = %v, want all 8", treeContents(t, dir))
	}
	if strings.Contains(body, `"content"`) {
		t.Fatalf("recursive listing without content=true returned content")
	}
}

func TestTreeLimitsFailWithoutPartialResult(t *testing.T) {
	root := writeTreeFixture(t)
	for _, query := range []string{
		"recursive=true&maxFiles=7",
		"recursive=true&content=true&patterns=*.json&maxBytes=10",
	} {
		code, _, body := getTree(t, root, query)
		if code != http.StatusUnprocessableEntity || !strings.Contains(body, "limit exceeded") {
			t.Fatalf("%s: HTTP %d: %s", query, code, body)
		}
	}
	// Exactly at the limits succeeds.
	if code, _, body := getTree(t, root, "recursive=true&maxFiles=8"); code != http.StatusOK {
		t.Fatalf("maxFiles=8: HTTP %d: %s", code, body)
	}
	if code, _, body := getTree(t, root, "recursive=true&content=true&patterns=a.json&maxBytes=7"); code != http.StatusOK {
		t.Fatalf("maxBytes=7: HTTP %d: %s", code, body)
	}
}

func TestTreeRejectsInvalidOptions(t *testing.T) {
	root := writeTreeFixture(t)
	for _, query := range []string{"recursive=yes", "content=1x", "maxFiles=0", "maxFiles=100001", "maxBytes=-1", "maxBytes=268435457"} {
		if code, _, body := getTree(t, root, query); code != http.StatusBadRequest {
			t.Fatalf("%s: HTTP %d, want 400: %s", query, code, body)
		}
	}
}

func TestTreeContentSkipsNonRegularFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("target"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "dir"), 0755); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{"file-link": "target.txt", "dir-link": "dir", "dangling": "missing"} {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}
	code, dir, body := getTree(t, root, "recursive=true&content=true")
	if code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", code, body)
	}
	got := treeContents(t, dir)
	want := map[string]string{"target.txt": "target", "file-link": "target", "dir-link": "<none>", "dangling": "<none>"}
	for path, content := range want {
		if got[path] != content {
			t.Fatalf("%s = %q, want %q (all: %v)", path, got[path], content, got)
		}
	}
}
