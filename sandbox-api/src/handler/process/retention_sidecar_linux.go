//go:build linux

package process

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// retentionSidecar stores the boundary when extended attributes are unavailable.
// Publish this companion file before punching holes in the log. The source
// file's OFD lock also serializes metadata updates, so no separate lock is needed.
type retentionSidecar struct {
	Version int    `json:"version"`
	Device  uint64 `json:"device"`
	Inode   uint64 `json:"inode"`
	Head    int64  `json:"head"`
}

func retentionSidecarPath(file *os.File) string { return file.Name() + ".retention" }

func retentionIdentity(file *os.File) (retentionSidecar, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return retentionSidecar{}, err
	}
	return retentionSidecar{Version: 1, Device: uint64(stat.Dev), Inode: stat.Ino}, nil
}

func readRetentionSidecar(file *os.File) (int64, bool, error) {
	fd, err := unix.Open(retentionSidecarPath(file), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	metadata := os.NewFile(uintptr(fd), retentionSidecarPath(file))
	defer metadata.Close()
	stat, err := metadata.Stat()
	if err != nil {
		return 0, false, err
	}
	if !stat.Mode().IsRegular() {
		return 0, false, fmt.Errorf("process log retention sidecar is not a regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(metadata, 513))
	if err != nil {
		return 0, false, err
	}
	var record retentionSidecar
	if len(raw) > 512 || json.Unmarshal(raw, &record) != nil || record.Version != 1 || record.Head < 0 {
		return 0, false, fmt.Errorf("invalid process log retention sidecar")
	}
	identity, err := retentionIdentity(file)
	if err != nil {
		return 0, false, err
	}
	if record.Device != identity.Device || record.Inode != identity.Inode {
		return 0, false, fmt.Errorf("process log retention sidecar does not match file identity")
	}
	return record.Head, true, nil
}

func writeRetentionSidecar(file *os.File, head int64) error {
	record, err := retentionIdentity(file)
	if err != nil {
		return err
	}
	record.Head = head
	// A descriptor can outlive its path. Never publish its boundary for a newer
	// file at the same path. Readers also reject mismatched device and inode values.
	current, err := os.Stat(file.Name())
	if err != nil {
		return err
	}
	original, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(current, original) {
		return fmt.Errorf("process log was replaced before retention publication")
	}
	path := retentionSidecarPath(file)
	// Reuse a fixed temporary path under the source file's OFD lock. After a
	// crash, the next write overwrites it instead of leaving another file behind.
	temporary := path + ".tmp"
	fd, err := unix.Open(temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_TRUNC|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	temp := os.NewFile(uintptr(fd), temporary)
	defer os.Remove(temporary)
	defer temp.Close()
	if err = json.NewEncoder(temp).Encode(record); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	if err = retentionIO.Load().rename(temp.Name(), path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func removeLogRetention(file *os.File) {
	// Do not remove metadata for a replacement that owns the same pathname.
	if _, found, err := readRetentionSidecar(file); err == nil && found {
		_ = os.Remove(retentionSidecarPath(file))
		_ = os.Remove(retentionSidecarPath(file) + ".tmp")
	}
}
