package backend

import (
	"time"
)

// EntryKind describes the kind of a backend entry.
type EntryKind int

const (
	EntryKindFile EntryKind = iota
	EntryKindDirectory
)

// Entry is backend-neutral metadata for files and directories.
type Entry struct {
	name    string
	size    int64
	kind    EntryKind
	modTime time.Time
}

// NewEntry creates immutable backend-neutral metadata for a file or directory.
func NewEntry(name string, size int64, kind EntryKind, modTime time.Time) *Entry {
	return &Entry{
		name:    name,
		size:    size,
		kind:    kind,
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

// Kind returns the entry kind.
func (e *Entry) Kind() EntryKind {
	return e.kind
}

// ModTime returns the entry modification time.
func (e *Entry) ModTime() time.Time {
	return e.modTime
}

// IsDir reports whether the entry describes a directory.
func (e *Entry) IsDir() bool {
	return e.kind == EntryKindDirectory
}
