package main

import (
	"bufio"
	"bytes"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"goftp/internal/pkg/auth/static"
	"goftp/internal/pkg/backend/filesystem"
	"goftp/internal/pkg/ftp"
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

func TestCleanVirtual(t *testing.T) {
	t.Parallel()

	s := &session{
		srv:        nil,
		conn:       nil,
		reader:     (*bufio.Reader)(nil),
		writer:     (*bufio.Writer)(nil),
		user:       "",
		loggedIn:   false,
		cwd:        "/docs",
		transfer:   "",
		pasv:       nil,
		renameFrom: "",
	}

	tests := map[string]string{
		"":                 "/docs",
		"file.txt":         "/docs/file.txt",
		"../file.txt":      "/file.txt",
		"../../file.txt":   "/file.txt",
		"/absolute.txt":    "/absolute.txt",
		"/nested/../a.txt": "/a.txt",
	}

	for input, want := range tests {
		got := s.cleanVirtual(input)
		if got != want {
			t.Fatalf("cleanVirtual(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestStorePreflightFailureRepliesBeforeTransfer(t *testing.T) {
	t.Parallel()

	storage, err := filesystem.NewFilesystemBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	var lc net.ListenConfig

	pasv, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = pasv.Close()
	}()

	var out bytes.Buffer

	s := &session{
		srv: &server{
			cfg: config{
				addr:     "",
				root:     "",
				user:     "",
				pass:     "",
				pasvHost: "",
			},
			authenticator: static.NewStaticAuthenticator("", ""),
			backend:       storage,
			ln:            nil,
			log:           log.New(os.Stdout, "", 0),
			wg:            sync.WaitGroup{},
		},
		conn:       nil,
		reader:     (*bufio.Reader)(nil),
		writer:     bufio.NewWriter(&out),
		user:       "",
		loggedIn:   true,
		cwd:        "/",
		transfer:   "",
		pasv:       pasv,
		renameFrom: "",
	}

	s.store("/missing/file.txt")

	got := out.String()

	wantPrefix := "550 "
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("store reply = %q, want prefix %q", got, wantPrefix)
	}

	if strings.HasPrefix(got, strconv.Itoa(int(ftp.ReplyFileStatusOK))) {
		t.Fatalf("store replied with transfer-start before preflight failure: %q", got)
	}
}
