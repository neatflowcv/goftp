package filesystem_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"goftp/internal/pkg/backend"
	"goftp/internal/pkg/backend/filesystem"
)

const goosWindows = "windows"

func TestCreateWriterKeepsTraversalInsideRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	b, err := filesystem.NewFilesystemBackend(root)
	if err != nil {
		t.Fatal(err)
	}

	writer, err := b.CreateWriter(context.Background(), "../../file.txt")
	if err != nil {
		t.Fatal(err)
	}

	_, err = writer.Write([]byte("content"))
	if err != nil {
		t.Fatal(err)
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(root, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != "content" {
		t.Fatalf("expected stored content, got %q", got)
	}
}

func TestStatRejectsSymlinkEscape(t *testing.T) {
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

	b, err := filesystem.NewFilesystemBackend(root)
	if err != nil {
		t.Fatal(err)
	}

	_, err = b.Stat(context.Background(), "/outside")
	if err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}

func TestCreateWriterRejectsSymlinkParentEscape(t *testing.T) {
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

	b, err := filesystem.NewFilesystemBackend(root)
	if err != nil {
		t.Fatal(err)
	}

	writer, err := b.CreateWriter(context.Background(), "/outside/new.txt")
	if err == nil {
		_ = writer.Close()

		t.Fatal("expected symlink parent escape to be rejected")
	}
}

func TestListSortsEntries(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, name := range []string{"b.txt", "a.txt"} {
		err := os.WriteFile(filepath.Join(root, name), nil, 0o644)
		if err != nil {
			t.Fatal(err)
		}
	}

	b, err := filesystem.NewFilesystemBackend(root)
	if err != nil {
		t.Fatal(err)
	}

	entries, err := b.List(context.Background(), "/")
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[0].Name() != "a.txt" || entries[1].Name() != "b.txt" {
		t.Fatalf("expected sorted entries, got %q then %q", entries[0].Name(), entries[1].Name())
	}

	if entries[0].Kind() != backend.EntryKindFile || entries[1].Kind() != backend.EntryKindFile {
		t.Fatalf("expected file entries, got %v then %v", entries[0].Kind(), entries[1].Kind())
	}
}

func TestListIgnoresUnsupportedEntries(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == goosWindows {
		t.Skip("symlink permissions vary on Windows")
	}

	root := t.TempDir()

	err := os.WriteFile(filepath.Join(root, "file.txt"), nil, 0o644)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(filepath.Join(root, "file.txt"), filepath.Join(root, "link.txt"))
	if err != nil {
		t.Fatal(err)
	}

	b, err := filesystem.NewFilesystemBackend(root)
	if err != nil {
		t.Fatal(err)
	}

	entries, err := b.List(context.Background(), "/")
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected only regular file entry, got %d entries", len(entries))
	}

	if entries[0].Name() != "file.txt" {
		t.Fatalf("expected symlink to be ignored, got %q", entries[0].Name())
	}
}
