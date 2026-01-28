package filesystem

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Memory implements FileSystem with an in-memory map for testing.
type Memory struct {
	mu    sync.RWMutex
	files map[string][]byte
	dirs  map[string]bool
}

func NewMemory() *Memory {
	return &Memory{
		files: make(map[string][]byte),
		dirs:  make(map[string]bool),
	}
}

func (m *Memory) MkdirAll(path string, perm os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	path = filepath.Clean(path)
	parts := strings.Split(path, string(filepath.Separator))
	current := ""
	for _, part := range parts {
		if part == "" {
			current = string(filepath.Separator)
			continue
		}
		current = filepath.Join(current, part)
		m.dirs[current] = true
	}
	return nil
}

func (m *Memory) WriteFile(path string, data []byte, perm os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Make a copy of data to avoid aliasing issues
	copied := make([]byte, len(data))
	copy(copied, data)
	m.files[filepath.Clean(path)] = copied
	return nil
}

func (m *Memory) ReadFile(path string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	path = filepath.Clean(path)
	if data, ok := m.files[path]; ok {
		copied := make([]byte, len(data))
		copy(copied, data)
		return copied, nil
	}
	return nil, os.ErrNotExist
}

func (m *Memory) RemoveAll(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	path = filepath.Clean(path)
	// Remove exact matches and anything under the path
	for f := range m.files {
		if f == path || strings.HasPrefix(f, path+string(filepath.Separator)) {
			delete(m.files, f)
		}
	}
	for d := range m.dirs {
		if d == path || strings.HasPrefix(d, path+string(filepath.Separator)) {
			delete(m.dirs, d)
		}
	}
	return nil
}

func (m *Memory) Stat(path string) (os.FileInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	path = filepath.Clean(path)
	if data, ok := m.files[path]; ok {
		return &memFileInfo{name: filepath.Base(path), size: int64(len(data)), isDir: false}, nil
	}
	if m.dirs[path] {
		return &memFileInfo{name: filepath.Base(path), isDir: true}, nil
	}
	return nil, os.ErrNotExist
}

// Files returns a copy of all files for test assertions.
func (m *Memory) Files() map[string][]byte {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string][]byte, len(m.files))
	for k, v := range m.files {
		copied := make([]byte, len(v))
		copy(copied, v)
		result[k] = copied
	}
	return result
}

// Dirs returns a copy of all directories for test assertions.
func (m *Memory) Dirs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]string, 0, len(m.dirs))
	for d := range m.dirs {
		result = append(result, d)
	}
	return result
}

type memFileInfo struct {
	name  string
	size  int64
	isDir bool
}

func (fi *memFileInfo) Name() string       { return fi.name }
func (fi *memFileInfo) Size() int64        { return fi.size }
func (fi *memFileInfo) Mode() os.FileMode  { return 0644 }
func (fi *memFileInfo) ModTime() time.Time { return time.Time{} }
func (fi *memFileInfo) IsDir() bool        { return fi.isDir }
func (fi *memFileInfo) Sys() interface{}   { return nil }
