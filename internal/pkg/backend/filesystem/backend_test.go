package filesystem_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"goftp/internal/pkg/backend"
	"goftp/internal/pkg/backend/filesystem"

	"github.com/stretchr/testify/require"
)

const goosWindows = "windows"

func TestCreateWriterKeepsTraversalInsideRoot(t *testing.T) {
	t.Parallel()

	// Arrange
	root := t.TempDir()
	b, err := filesystem.NewFilesystemBackend(root)
	require.NoError(t, err)

	// Act
	writer, err := b.CreateWriter(context.Background(), "../../file.txt")

	// Assert
	require.NoError(t, err)
	requireWriterStoresContent(t, writer, filepath.Join(root, "file.txt"), "content")
}

func TestStatRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == goosWindows {
		t.Skip("symlink permissions vary on Windows")
	}

	// Arrange
	root := t.TempDir()
	outside := t.TempDir()

	err := os.Symlink(outside, filepath.Join(root, "outside"))
	require.NoError(t, err)

	b, err := filesystem.NewFilesystemBackend(root)
	require.NoError(t, err)

	// Act
	_, err = b.Stat(context.Background(), "/outside")

	// Assert
	require.Error(t, err)
}

func TestCreateWriterRejectsSymlinkParentEscape(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == goosWindows {
		t.Skip("symlink permissions vary on Windows")
	}

	// Arrange
	root := t.TempDir()
	outside := t.TempDir()

	err := os.Symlink(outside, filepath.Join(root, "outside"))
	require.NoError(t, err)

	b, err := filesystem.NewFilesystemBackend(root)
	require.NoError(t, err)

	// Act
	writer, err := b.CreateWriter(context.Background(), "/outside/new.txt")

	// Assert
	require.Error(t, err)
	require.Nil(t, writer)
}

func TestListSortsEntries(t *testing.T) {
	t.Parallel()

	// Arrange
	root := t.TempDir()
	for _, name := range []string{"b.txt", "a.txt"} {
		err := os.WriteFile(filepath.Join(root, name), nil, 0o644)
		require.NoError(t, err)
	}

	b, err := filesystem.NewFilesystemBackend(root)
	require.NoError(t, err)

	// Act
	entries, err := b.List(context.Background(), "/")

	// Assert
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, "a.txt", entries[0].Name())
	require.Equal(t, "b.txt", entries[1].Name())
	require.Equal(t, backend.EntryKindFile, entries[0].Kind())
	require.Equal(t, backend.EntryKindFile, entries[1].Kind())
}

func TestListIgnoresUnsupportedEntries(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == goosWindows {
		t.Skip("symlink permissions vary on Windows")
	}

	// Arrange
	root := t.TempDir()

	err := os.WriteFile(filepath.Join(root, "file.txt"), nil, 0o644)
	require.NoError(t, err)

	err = os.Symlink(filepath.Join(root, "file.txt"), filepath.Join(root, "link.txt"))
	require.NoError(t, err)

	b, err := filesystem.NewFilesystemBackend(root)
	require.NoError(t, err)

	// Act
	entries, err := b.List(context.Background(), "/")

	// Assert
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "file.txt", entries[0].Name())
}

func requireWriterStoresContent(t *testing.T, writer io.WriteCloser, path, content string) {
	t.Helper()

	require.NotNil(t, writer)

	_, err := writer.Write([]byte(content))
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, content, string(got))
}
