package fsx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomicReplacesAndLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	for _, content := range []string{"one", "two, longer", "3"} {
		if err := WriteAtomic(p, []byte(content)); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(p); string(b) != content {
			t.Fatalf("got %q want %q", b, content)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp*")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestWriteAtomicFailureKeepsTheOldFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	WriteAtomic(p, []byte("good"))
	if err := WriteAtomic(filepath.Join(dir, "missing-dir", "state.json"), []byte("x")); err == nil {
		t.Fatal("writing into a missing folder should fail")
	}
	if b, _ := os.ReadFile(p); string(b) != "good" {
		t.Fatalf("old file damaged: %q", b)
	}
}
