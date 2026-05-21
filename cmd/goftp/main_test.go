package main

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestSplitCommand(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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

	err = file.Close()
	if err != nil {
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
	t.Parallel()

	if runtime.GOOS == goosWindows {
		t.Skip("symlink permissions vary on Windows")
	}

	root := t.TempDir()

	outside := t.TempDir()

	err := os.Symlink(outside, filepath.Join(root, "outside"))
	if err != nil {
		t.Fatal(err)
	}

	s := testSession(t, root)

	_, err = s.realPath("/outside", true)
	if err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}

func TestRealPathForCreateRejectsSymlinkParentEscape(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == goosWindows {
		t.Skip("symlink permissions vary on Windows")
	}

	root := t.TempDir()

	outside := t.TempDir()

	err := os.Symlink(outside, filepath.Join(root, "outside"))
	if err != nil {
		t.Fatal(err)
	}

	s := testSession(t, root)

	_, err = s.realPathForCreate("/outside/new.txt")
	if err == nil {
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
			cfg: config{
				addr:     "",
				root:     "",
				user:     "",
				pass:     "",
				pasvHost: "",
			},
			rootAbs: rootAbs,
			ln:      nil,
			log:     nil,
			wg:      sync.WaitGroup{},
		},
		conn:       nil,
		reader:     nil,
		writer:     nil,
		user:       "",
		loggedIn:   false,
		cwd:        "/",
		transfer:   "",
		pasv:       nil,
		renameFrom: "",
	}
}
