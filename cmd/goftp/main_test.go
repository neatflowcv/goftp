package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSplitCommand(t *testing.T) {
	cmd, arg := splitCommand("user  alice  ")
	if cmd != "USER" || arg != "alice" {
		t.Fatalf("splitCommand returned %q, %q", cmd, arg)
	}

	cmd, arg = splitCommand("NOOP")
	if cmd != "NOOP" || arg != "" {
		t.Fatalf("splitCommand returned %q, %q", cmd, arg)
	}
}

func TestRealPathStaysInsideRoot(t *testing.T) {
	root := t.TempDir()
	s := testSession(t, root)

	got, err := s.realPath(s.cleanVirtual("../../etc/passwd"), false)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "etc", "passwd")
	if got != want {
		t.Fatalf("expected traversal to stay under root as %q, got %q", want, got)
	}

	file, err := os.Create(filepath.Join(root, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	got, err = s.realPath("/file.txt", true)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(root, "file.txt") {
		t.Fatalf("expected file path inside root, got %q", got)
	}
}

func TestRealPathRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}

	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}

	s := testSession(t, root)
	if _, err := s.realPath("/outside", true); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}

func TestRealPathForCreateRejectsSymlinkParentEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}

	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}

	s := testSession(t, root)
	if _, err := s.realPathForCreate("/outside/new.txt"); err == nil {
		t.Fatal("expected symlink parent escape to be rejected")
	}
}

func testSession(t *testing.T, root string) *session {
	t.Helper()

	rootAbs, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return &session{
		srv: &server{
			rootAbs: rootAbs,
		},
		cwd: "/",
	}
}
