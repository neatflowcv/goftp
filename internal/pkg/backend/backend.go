package backend

import (
	"context"
	"io"
)

// Backend defines storage operations over FTP virtual absolute paths.
type Backend interface {
	Stat(ctx context.Context, path string) (*Entry, error)
	List(ctx context.Context, path string) ([]*Entry, error)
	OpenReader(ctx context.Context, path string) (io.ReadCloser, error)
	CreateWriter(ctx context.Context, path string) (io.WriteCloser, error)
	DeleteFile(ctx context.Context, path string) error
	MakeDir(ctx context.Context, path string) error
	RemoveDir(ctx context.Context, path string) error
	Rename(ctx context.Context, fromPath, toPath string) error
}
