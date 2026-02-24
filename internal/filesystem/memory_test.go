package filesystem

import (
	"os"
	"testing"
)

func TestMemory_WriteAndRead(t *testing.T) {
	m := NewMemory()

	err := m.WriteFile("/test/file.txt", []byte("hello"), 0644)
	if err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	files := m.Files()
	if string(files["/test/file.txt"]) != "hello" {
		t.Errorf("got %q, want %q", string(files["/test/file.txt"]), "hello")
	}
}

func TestMemory_MkdirAll(t *testing.T) {
	m := NewMemory()

	err := m.MkdirAll("/a/b/c", 0755)
	if err != nil {
		t.Fatalf("MkdirAll error: %v", err)
	}

	dirs := m.Dirs()
	want := map[string]bool{"/a": true, "/a/b": true, "/a/b/c": true}
	for _, d := range dirs {
		if !want[d] {
			t.Errorf("unexpected dir: %s", d)
		}
		delete(want, d)
	}
	for d := range want {
		t.Errorf("missing dir: %s", d)
	}
}

func TestMemory_RemoveAll(t *testing.T) {
	m := NewMemory()

	m.MkdirAll("/a/b/c", 0755)
	m.WriteFile("/a/b/file.txt", []byte("test"), 0644)
	m.WriteFile("/a/b/c/nested.txt", []byte("nested"), 0644)
	m.WriteFile("/other.txt", []byte("other"), 0644)

	err := m.RemoveAll("/a/b")
	if err != nil {
		t.Fatalf("RemoveAll error: %v", err)
	}

	files := m.Files()
	if _, ok := files["/a/b/file.txt"]; ok {
		t.Error("file /a/b/file.txt should be removed")
	}
	if _, ok := files["/a/b/c/nested.txt"]; ok {
		t.Error("file /a/b/c/nested.txt should be removed")
	}
	if _, ok := files["/other.txt"]; !ok {
		t.Error("file /other.txt should still exist")
	}
}

func TestMemory_Stat(t *testing.T) {
	m := NewMemory()

	m.MkdirAll("/dir", 0755)
	m.WriteFile("/dir/file.txt", []byte("content"), 0644)

	// Stat file
	fi, err := m.Stat("/dir/file.txt")
	if err != nil {
		t.Fatalf("Stat file error: %v", err)
	}
	if fi.IsDir() {
		t.Error("file should not be a directory")
	}
	if fi.Size() != 7 {
		t.Errorf("size = %d, want 7", fi.Size())
	}

	// Stat directory
	fi, err = m.Stat("/dir")
	if err != nil {
		t.Fatalf("Stat dir error: %v", err)
	}
	if !fi.IsDir() {
		t.Error("dir should be a directory")
	}

	// Stat non-existent
	_, err = m.Stat("/nonexistent")
	if err != os.ErrNotExist {
		t.Errorf("expected ErrNotExist, got %v", err)
	}
}

func TestMemory_ReadDir(t *testing.T) {
	m := NewMemory()

	m.MkdirAll("/root/subdir", 0755)
	m.WriteFile("/root/file-a.txt", []byte("aaa"), 0644)
	m.WriteFile("/root/file-b.txt", []byte("bb"), 0644)
	m.WriteFile("/root/subdir/nested.txt", []byte("nested"), 0644)

	entries, err := m.ReadDir("/root")
	if err != nil {
		t.Fatalf("ReadDir error: %v", err)
	}

	if len(entries) != 3 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("expected 3 entries, got %d: %v", len(entries), names)
	}

	// Entries should be sorted by name
	if entries[0].Name() != "file-a.txt" || entries[0].IsDir() {
		t.Errorf("entry[0] = %s (dir=%v), want file-a.txt (dir=false)", entries[0].Name(), entries[0].IsDir())
	}
	if entries[1].Name() != "file-b.txt" || entries[1].IsDir() {
		t.Errorf("entry[1] = %s (dir=%v), want file-b.txt (dir=false)", entries[1].Name(), entries[1].IsDir())
	}
	if entries[2].Name() != "subdir" || !entries[2].IsDir() {
		t.Errorf("entry[2] = %s (dir=%v), want subdir (dir=true)", entries[2].Name(), entries[2].IsDir())
	}

	// ReadDir on nonexistent path
	_, err = m.ReadDir("/nonexistent")
	if err != os.ErrNotExist {
		t.Errorf("expected ErrNotExist, got %v", err)
	}

	// ReadDir on empty dir
	m.MkdirAll("/empty", 0755)
	entries, err = m.ReadDir("/empty")
	if err != nil {
		t.Fatalf("ReadDir empty error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for empty dir, got %d", len(entries))
	}
}
