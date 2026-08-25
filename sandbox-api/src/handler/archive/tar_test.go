package archive

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestArchiveSizeMatchesArchive is the property the upload depends on: a
// presigned PUT needs a Content-Length before the first byte is sent, and the
// only way to know it without staging the archive is to size it by writing the
// same archive with zeroed content.
func TestArchiveSizeMatchesArchive(t *testing.T) {
	root, lower := fakeSandbox(t)
	write(t, filepath.Join(root, "usr/bin/curl"), "ELF binary content", 0o755)
	write(t, filepath.Join(root, "etc/apk/world"), "curl\nrsync\n", 0o644)
	// A long path, so the archive uses PAX records and the size is not simply a
	// multiple of the header block.
	write(t, filepath.Join(root, "home/user/"+string(bytes.Repeat([]byte("a"), 120))+"/file"), "x", 0o644)
	if err := os.Symlink("busybox", filepath.Join(root, "bin")); err != nil {
		t.Fatal(err)
	}

	changes, err := Diff(root, lower, nil)
	if err != nil {
		t.Fatal(err)
	}
	metadata := []member{{name: ManifestName, data: []byte(`{"version":1}`)}}

	predicted, err := writeArchive(io.Discard, root, metadata, changes, false)
	if err != nil {
		t.Fatalf("sizing failed: %v", err)
	}

	var archived bytes.Buffer
	written, err := writeArchive(&archived, root, metadata, changes, true)
	if err != nil {
		t.Fatalf("archiving failed: %v", err)
	}

	if predicted != written || predicted != int64(archived.Len()) {
		t.Fatalf("predicted %d bytes, wrote %d bytes (buffer %d)", predicted, written, archived.Len())
	}
}

func TestArchiveContent(t *testing.T) {
	root, lower := fakeSandbox(t)
	write(t, filepath.Join(root, "usr/bin/curl"), "ELF", 0o755)
	write(t, filepath.Join(lower, "etc/rsyncd.conf"), "conf", 0o644)
	if err := os.Symlink("bash", filepath.Join(root, "bin")); err != nil {
		t.Fatal(err)
	}

	changes, err := Diff(root, lower, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(Manifest{Version: ManifestVersion, Deleted: []string{"etc/rsyncd.conf"}})
	if err != nil {
		t.Fatal(err)
	}

	var archived bytes.Buffer
	if _, err := writeArchive(&archived, root, []member{{name: ManifestName, data: manifest}}, changes, true); err != nil {
		t.Fatal(err)
	}

	reader := tar.NewReader(&archived)
	members := map[string]*tar.Header{}
	var first string
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if first == "" {
			first = header.Name
		}
		members[header.Name] = header
		if header.Name == ManifestName {
			var decoded Manifest
			if err := json.NewDecoder(reader).Decode(&decoded); err != nil {
				t.Fatalf("manifest is not readable: %v", err)
			}
			if len(decoded.Deleted) != 1 || decoded.Deleted[0] != "etc/rsyncd.conf" {
				t.Errorf("expected the deletion to travel in the manifest, got %v", decoded.Deleted)
			}
		}
	}

	if first != ManifestName {
		t.Errorf("expected the manifest to be the first member so a reader can decide before the payload, got %s", first)
	}
	if header := members["usr/bin/curl"]; header == nil {
		t.Error("expected the added file to be archived")
	} else {
		if header.Mode&0o111 == 0 {
			t.Errorf("expected the executable bit to be preserved, got mode %o", header.Mode)
		}
		// Access and change times would travel as PAX records whose length
		// depends on the value, and reading a file to archive it changes its
		// access time: the header has to be independent of when it is written,
		// since its length was already announced to the storage.
		if !header.AccessTime.IsZero() || !header.ChangeTime.IsZero() {
			t.Errorf("expected no access/change time in the header, got %v and %v", header.AccessTime, header.ChangeTime)
		}
		if header.ModTime.Nanosecond() != 0 {
			t.Errorf("expected a whole-second mtime, got %v", header.ModTime)
		}
	}
	if header := members["bin"]; header == nil || header.Linkname != "bash" {
		t.Errorf("expected the symlink to be archived with its target, got %+v", header)
	}
	if _, present := members["etc/rsyncd.conf"]; present {
		t.Error("expected a deleted path not to be an archive member")
	}
	if header := members["usr/"]; header == nil || header.Typeflag != tar.TypeDir {
		t.Error("expected added directories to be archived so their mode is restored")
	}
}
