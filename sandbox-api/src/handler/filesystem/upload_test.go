package filesystem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadPreservesExistingAndReplacedInodes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	fs := NewFilesystem(dir)
	creator, err := fs.WriteUpload(path, strings.NewReader("first"))
	if err != nil {
		t.Fatal(err)
	}
	defer creator.Close()
	if err := creator.SetPermissions(0600); err != nil {
		t.Fatal(err)
	}
	other, err := fs.WriteUpload(path, strings.NewReader("second"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if other.created {
		t.Fatal("second open claimed creation")
	}
	if err := other.SetPermissions(0777); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("existing mode: %o", info.Mode().Perm())
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := creator.SetPermissions(0777); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("chmod followed replaced path")
	}
}
