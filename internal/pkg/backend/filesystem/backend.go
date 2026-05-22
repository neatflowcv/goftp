package filesystem

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"goftp/internal/pkg/backend"
)

const goosWindows = "windows"

var (
	errNotDirectory       = errors.New("not a directory")
	errParentNotDirectory = errors.New("parent is not a directory")
	errPathEscapesRoot    = errors.New("path escapes FTP root")
	errRootNotDirectory   = errors.New("root is not a directory")
	errUnsupportedEntry   = errors.New("unsupported entry type")
)

var _ backend.Backend = (*Backend)(nil)

// Backend stores FTP paths on a local filesystem rooted at rootAbs.
type Backend struct {
	rootAbs string
}

// NewFilesystemBackend creates a filesystem backend rooted at root.
func NewFilesystemBackend(root string) (*Backend, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	rootAbs, err = filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}

	info, err := os.Stat(rootAbs)
	if err != nil {
		return nil, fmt.Errorf("stat root: %w", err)
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s", errRootNotDirectory, rootAbs)
	}

	return &Backend{rootAbs: rootAbs}, nil
}

// Stat returns metadata for path.
func (b *Backend) Stat(ctx context.Context, virtualPath string) (*backend.Entry, error) {
	err := ctx.Err()
	if err != nil {
		return nil, err
	}

	fsPath, err := b.realPath(virtualPath)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(fsPath)
	if err != nil {
		return nil, err
	}

	entry, ok := entryFromFileInfo(info)
	if !ok {
		return nil, errUnsupportedEntry
	}

	return entry, nil
}

// List returns child entries for a directory.
func (b *Backend) List(ctx context.Context, virtualPath string) ([]*backend.Entry, error) {
	err := ctx.Err()
	if err != nil {
		return nil, err
	}

	fsPath, err := b.realPath(virtualPath)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(fsPath)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		return nil, errNotDirectory
	}

	dirEntries, err := os.ReadDir(fsPath)
	if err != nil {
		return nil, err
	}

	sort.Slice(dirEntries, func(i, j int) bool {
		return dirEntries[i].Name() < dirEntries[j].Name()
	})

	entries := make([]*backend.Entry, 0, len(dirEntries))
	for _, dirEntry := range dirEntries {
		info, err := dirEntry.Info()
		if err != nil {
			return nil, err
		}

		entry, ok := entryFromFileInfo(info)
		if !ok {
			continue
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

// OpenReader opens path for reading.
func (b *Backend) OpenReader(ctx context.Context, virtualPath string) (io.ReadCloser, error) {
	err := ctx.Err()
	if err != nil {
		return nil, err
	}

	fsPath, err := b.realPath(virtualPath)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(fsPath)
	if err != nil {
		return nil, err
	}

	if info.IsDir() {
		return nil, errNotDirectory
	}

	return os.Open(fsPath)
}

// CreateWriter opens path for writing, replacing any existing file.
func (b *Backend) CreateWriter(ctx context.Context, virtualPath string) (io.WriteCloser, error) {
	err := ctx.Err()
	if err != nil {
		return nil, err
	}

	fsPath, err := b.realPathForCreate(virtualPath)
	if err != nil {
		return nil, err
	}

	file, err := os.OpenFile(fsPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}

	return file, nil
}

// DeleteFile removes a file.
func (b *Backend) DeleteFile(ctx context.Context, virtualPath string) error {
	err := ctx.Err()
	if err != nil {
		return err
	}

	fsPath, err := b.realPath(virtualPath)
	if err != nil {
		return err
	}

	info, err := os.Stat(fsPath)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return errNotDirectory
	}

	return os.Remove(fsPath)
}

// MakeDir creates a directory.
func (b *Backend) MakeDir(ctx context.Context, virtualPath string) error {
	err := ctx.Err()
	if err != nil {
		return err
	}

	fsPath, err := b.realPathForCreate(virtualPath)
	if err != nil {
		return err
	}

	return os.Mkdir(fsPath, 0o755)
}

// RemoveDir removes a directory.
func (b *Backend) RemoveDir(ctx context.Context, virtualPath string) error {
	err := ctx.Err()
	if err != nil {
		return err
	}

	fsPath, err := b.realPath(virtualPath)
	if err != nil {
		return err
	}

	info, err := os.Stat(fsPath)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		return errNotDirectory
	}

	return os.Remove(fsPath)
}

// Rename moves fromPath to toPath.
func (b *Backend) Rename(ctx context.Context, fromPath, toPath string) error {
	err := ctx.Err()
	if err != nil {
		return err
	}

	from, err := b.realPath(fromPath)
	if err != nil {
		return err
	}

	to, err := b.realPathForCreate(toPath)
	if err != nil {
		return err
	}

	return os.Rename(from, to)
}

func entryFromFileInfo(info os.FileInfo) (*backend.Entry, bool) {
	switch {
	case info.Mode().IsRegular():
		return backend.NewEntry(info.Name(), info.Size(), backend.EntryKindFile, info.ModTime()), true
	case info.IsDir():
		return backend.NewEntry(info.Name(), info.Size(), backend.EntryKindDirectory, info.ModTime()), true
	default:
		return nil, false
	}
}

func (b *Backend) realPath(virtualPath string) (string, error) {
	cleanVirtual := cleanVirtual(virtualPath)
	fsPath := filepath.Join(b.rootAbs, filepath.FromSlash(strings.TrimPrefix(cleanVirtual, "/")))

	fsPath = filepath.Clean(fsPath)

	eval, err := filepath.EvalSymlinks(fsPath)
	if err != nil {
		return "", err
	}

	fsPath = eval
	if !insideRoot(b.rootAbs, fsPath) {
		return "", errPathEscapesRoot
	}

	return fsPath, nil
}

func (b *Backend) realPathForCreate(virtualPath string) (string, error) {
	cleanVirtual := cleanVirtual(virtualPath)
	parentVirtual := path.Dir(cleanVirtual)

	parentReal, err := b.realPath(parentVirtual)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(parentReal)
	if err != nil {
		return "", err
	}

	if !info.IsDir() {
		return "", errParentNotDirectory
	}

	fsPath := filepath.Join(parentReal, path.Base(cleanVirtual))

	fsPath = filepath.Clean(fsPath)
	if !insideRoot(b.rootAbs, fsPath) {
		return "", errPathEscapesRoot
	}

	return fsPath, nil
}

func cleanVirtual(virtualPath string) string {
	return path.Clean("/" + strings.TrimPrefix(virtualPath, "/"))
}

func insideRoot(root, candidate string) bool {
	if runtime.GOOS == goosWindows {
		root = strings.ToLower(root)
		candidate = strings.ToLower(candidate)
	}

	if candidate == root {
		return true
	}

	sepRoot := strings.TrimRight(root, string(filepath.Separator)) + string(filepath.Separator)

	return strings.HasPrefix(candidate, sepRoot)
}
