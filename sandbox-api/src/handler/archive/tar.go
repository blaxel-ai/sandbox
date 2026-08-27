package archive

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// MetadataDir holds the archive's own members, the ones that describe the
// archive rather than belong to the sandbox filesystem. It is not a path the
// image has, and import must not restore it as filesystem content.
const MetadataDir = ".blaxel-archive"

const (
	// ManifestName describes what the archive contains and what it was taken
	// from. It is the first member so a reader can decide before the payload.
	ManifestName = MetadataDir + "/manifest.json"
	// ProcessesName is the process manager state, byte for byte what the hot
	// upgrade path writes, so restore can relaunch the workload.
	ProcessesName = MetadataDir + "/processes.json"
)

// ManifestVersion is the archive format version. Import refuses an archive it
// does not know how to read.
const ManifestVersion = 1

// Manifest describes an archive.
type Manifest struct {
	Version   int       `json:"version" binding:"required" example:"1"`
	CreatedAt time.Time `json:"createdAt" binding:"required"`
	// APIVersion is the sandbox-api build that produced the archive.
	APIVersion string `json:"apiVersion,omitempty" example:"v0.1.0"`
	// ImageDevice is the device the pristine image was read from, for the record:
	// it says where the sandbox that exported the archive found its image.
	ImageDevice string `json:"imageDevice,omitempty" example:"/dev/vda"`
	// Root is the directory the paths are relative to.
	Root string `json:"root" binding:"required" example:"/"`
	// Excludes are the paths left out of the comparison.
	Excludes []string `json:"excludes,omitempty"`
	// Added and Modified count the archive's payload members.
	Added    int `json:"added" example:"11"`
	Modified int `json:"modified" example:"3"`
	// Deleted are paths the image has and the sandbox deleted. Tar cannot carry
	// a deletion, so import applies these from the manifest.
	Deleted []string `json:"deleted,omitempty"`
	// PayloadBytes is the total content size of the payload members.
	PayloadBytes int64 `json:"payloadBytes" example:"3073449"`
	// Processes tells whether ProcessesName is present.
	Processes bool `json:"processes" example:"true"`
} // @name ArchiveManifest

// member is an archive member whose content is held in memory: the manifest and
// the process state, both small and both produced by this process.
type member struct {
	name string
	data []byte
}

// writeArchive writes the archive to w: the metadata members first, then one
// member per added or modified path, read from root.
//
// The layout depends only on the headers and the recorded sizes, so calling it
// with a discarding body reader yields the exact byte count the real call will
// produce. Size returns that count. Content is written to w as it is read, so
// the archive is never staged.
func writeArchive(w io.Writer, root string, metadata []member, changes []Change, readContent bool) (int64, error) {
	counter := &countingWriter{w: w}
	tw := tar.NewWriter(counter)

	// Whole seconds: a sub-second timestamp is not representable in a tar header
	// and becomes a PAX record whose length varies with the value, which would
	// make the sizing pass and the streaming pass disagree.
	now := time.Now().Truncate(time.Second)

	for _, meta := range metadata {
		header := &tar.Header{
			Name:     meta.name,
			Mode:     0o600,
			Size:     int64(len(meta.data)),
			ModTime:  now,
			Typeflag: tar.TypeReg,
			Format:   tar.FormatPAX,
		}
		if err := tw.WriteHeader(header); err != nil {
			return 0, fmt.Errorf("failed to write header for %s: %w", meta.name, err)
		}
		if _, err := tw.Write(meta.data); err != nil {
			return 0, fmt.Errorf("failed to write %s: %w", meta.name, err)
		}
	}

	for _, change := range changes {
		if change.Kind == ChangeDeleted {
			continue
		}
		if err := writeEntry(tw, root, change, readContent); err != nil {
			return 0, err
		}
	}

	if err := tw.Close(); err != nil {
		return 0, fmt.Errorf("failed to close archive: %w", err)
	}
	return counter.count, nil
}

// writeEntry writes one filesystem path. It always writes exactly the size
// recorded during the scan: a file that grew or shrank since would otherwise
// desynchronise the archive from the length already announced to S3.
func writeEntry(tw *tar.Writer, root string, change Change, readContent bool) error {
	info := change.info
	if info == nil {
		var err error
		info, err = os.Lstat(filepath.Join(root, change.Path))
		if err != nil {
			return fmt.Errorf("failed to stat %s: %w", change.Path, err)
		}
	}

	link := ""
	if info.Mode()&os.ModeSymlink != 0 {
		var err error
		link, err = os.Readlink(filepath.Join(root, change.Path))
		if err != nil {
			return fmt.Errorf("failed to read symlink %s: %w", change.Path, err)
		}
	}

	header, err := tar.FileInfoHeader(info, link)
	if err != nil {
		return fmt.Errorf("failed to build header for %s: %w", change.Path, err)
	}
	header.Name = change.Path
	if info.IsDir() {
		header.Name += "/"
	}
	header.Format = tar.FormatPAX
	// Timestamps that are not whole seconds travel as PAX records whose length
	// depends on the value, and the access time of a file changes as it is read
	// to archive it. The header has to be the same whether it is written to size
	// the archive or to upload it, since the length is announced up front.
	header.ModTime = header.ModTime.Truncate(time.Second)
	header.AccessTime = time.Time{}
	header.ChangeTime = time.Time{}
	header.Size = 0
	if info.Mode().IsRegular() {
		header.Size = change.Size
	}

	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("failed to write header for %s: %w", change.Path, err)
	}
	if header.Size == 0 {
		return nil
	}

	if !readContent {
		if _, err := io.CopyN(tw, zeroReader{}, header.Size); err != nil {
			return fmt.Errorf("failed to size %s: %w", change.Path, err)
		}
		return nil
	}

	file, err := os.Open(filepath.Join(root, change.Path))
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", change.Path, err)
	}
	defer file.Close()

	written, err := io.CopyN(tw, file, header.Size)
	if err != nil && err != io.EOF {
		return fmt.Errorf("failed to archive %s: %w", change.Path, err)
	}
	if written < header.Size {
		// The file shrank between the scan and now. Pad so the member matches
		// its header, and so the total matches the announced length.
		if _, err := io.CopyN(tw, zeroReader{}, header.Size-written); err != nil {
			return fmt.Errorf("failed to pad %s: %w", change.Path, err)
		}
	}
	return nil
}

// countingWriter counts what it forwards.
type countingWriter struct {
	w     io.Writer
	count int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.count += int64(n)
	return n, err
}

// zeroReader is an endless source of zeros, used to size the archive without
// reading the files.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}
