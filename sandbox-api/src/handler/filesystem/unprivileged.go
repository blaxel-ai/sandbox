package filesystem

import (
	"io"
	"os"
	"path/filepath"

	"github.com/blaxel-ai/sandbox-api/src/lib/identity"
)

// Every exported filesystem operation runs through identity.Do, so the kernel
// checks each path against the workload user instead of the API's root
// identity. Without it the filesystem endpoints would be an escalation path out
// of the unprivileged execution model: a process could ask the API to overwrite
// a root-owned binary (blfs, the sandbox-api itself, a login shell) and get its
// code run as root by the next privileged operation.
//
// The lowercase methods hold the actual implementation and call each other, so
// no operation nests the identity switch more than once.

func (fs *Filesystem) FileExists(path string) (bool, error) {
	var exists bool
	err := identity.Do(func() error {
		var err error
		exists, err = fs.fileExists(path)
		return err
	})
	return exists, err
}

func (fs *Filesystem) DirectoryExists(path string) (bool, error) {
	var exists bool
	err := identity.Do(func() error {
		var err error
		exists, err = fs.directoryExists(path)
		return err
	})
	return exists, err
}

func (fs *Filesystem) Infos(path string) (os.FileInfo, error) {
	var info os.FileInfo
	err := identity.Do(func() error {
		var err error
		info, err = fs.infos(path)
		return err
	})
	return info, err
}

func (fs *Filesystem) ReadFile(path string) (*FileWithContentByte, error) {
	var file *FileWithContentByte
	err := identity.Do(func() error {
		var err error
		file, err = fs.readFile(path)
		return err
	})
	return file, err
}

func (fs *Filesystem) WriteFile(path string, content []byte, perm os.FileMode) error {
	return identity.Do(func() error {
		return fs.writeFile(path, content, perm)
	})
}

func (fs *Filesystem) WriteFileFromReader(path string, r io.Reader, perm os.FileMode) error {
	return identity.Do(func() error {
		return fs.writeFileFromReader(path, r, perm)
	})
}

func (fs *Filesystem) CreateDirectory(path string, perm os.FileMode) error {
	return identity.Do(func() error {
		return fs.createDirectory(path, perm)
	})
}

func (fs *Filesystem) ListDirectory(path string) (*Directory, error) {
	var dir *Directory
	err := identity.Do(func() error {
		var err error
		dir, err = fs.listDirectory(path)
		return err
	})
	return dir, err
}

func (fs *Filesystem) DeleteFile(path string) error {
	return identity.Do(func() error {
		return fs.deleteFile(path)
	})
}

func (fs *Filesystem) DeleteDirectory(path string, recursive bool) error {
	return identity.Do(func() error {
		return fs.deleteDirectory(path, recursive)
	})
}

func (fs *Filesystem) CopyFile(src, dst string) error {
	return identity.Do(func() error {
		return fs.copyFile(src, dst)
	})
}

func (fs *Filesystem) MoveFile(src, dst string) error {
	return identity.Do(func() error {
		return fs.moveFile(src, dst)
	})
}

func (fs *Filesystem) GetFileInfo(path string) (*FileByte, error) {
	var file *FileByte
	err := identity.Do(func() error {
		var err error
		file, err = fs.getFileInfo(path)
		return err
	})
	return file, err
}

// Walk keeps the identity applied for the whole traversal, including the calls
// to fn, so a callback reading the files it is given is checked the same way.
func (fs *Filesystem) Walk(root string, fn filepath.WalkFunc) error {
	return identity.Do(func() error {
		return fs.walk(root, fn)
	})
}

func (fs *Filesystem) CreateOrUpdateFile(path string, content string, isDirectory bool, permissions string) error {
	return identity.Do(func() error {
		return fs.createOrUpdateFile(path, content, isDirectory, permissions)
	})
}
