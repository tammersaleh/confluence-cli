package filesystem

import (
	"os"
)

type FileSystem interface {
	MkdirAll(path string, perm os.FileMode) error
	WriteFile(path string, data []byte, perm os.FileMode) error
	RemoveAll(path string) error
	Stat(path string) (os.FileInfo, error)
}

// OS implements FileSystem using the real filesystem.
type OS struct{}

func (OS) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (OS) WriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

func (OS) RemoveAll(path string) error {
	return os.RemoveAll(path)
}

func (OS) Stat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}
