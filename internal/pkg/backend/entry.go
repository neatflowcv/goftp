package backend

import (
	"io/fs"
	"time"
)

// Entry is backend-neutral metadata for files and directories.
type Entry struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
}

// NewEntry creates immutable backend-neutral metadata for a file or directory.
func NewEntry(name string, size int64, mode fs.FileMode, modTime time.Time) *Entry {
	return &Entry{
		name:    name,
		size:    size,
		mode:    mode,
		modTime: modTime,
	}
}

// Name returns the base name of the entry.
func (e *Entry) Name() string {
	return e.name
}

// Size returns the entry size in bytes.
func (e *Entry) Size() int64 {
	return e.size
}

// Mode returns the entry mode.
func (e *Entry) Mode() fs.FileMode {
	return e.mode
}

// ModTime returns the entry modification time.
func (e *Entry) ModTime() time.Time {
	return e.modTime
}

// IsDir reports whether the entry describes a directory.
func (e *Entry) IsDir() bool {
	return e.mode.IsDir()
}
