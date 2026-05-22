package main

import (
	"bufio"
	"bytes"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

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
		mu:         sync.Mutex{},
		srv:        nil,
		conn:       nil,
		reader:     (*bufio.Reader)(nil),
		writer:     (*bufio.Writer)(nil),
		user:       "",
		loggedIn:   false,
		cwd:        "/docs",
		transfer:   "",
		pasv:       nil,
		data:       nil,
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
		mu:         sync.Mutex{},
		srv:        newTestServer(storage, nil),
		conn:       nil,
		reader:     (*bufio.Reader)(nil),
		writer:     bufio.NewWriter(&out),
		user:       "",
		loggedIn:   true,
		cwd:        "/",
		transfer:   "",
		pasv:       pasv,
		data:       nil,
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

func TestServerShutdownClosesActiveSession(t *testing.T) {
	t.Parallel()

	storage, err := filesystem.NewFilesystemBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(storage, ln)

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.serve()
	}()

	var dialer net.Dialer

	conn, err := dialer.DialContext(t.Context(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close()
	}()

	reader := bufio.NewReader(conn)

	greeting, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(greeting, strconv.Itoa(int(ftp.ReplyServiceReady))) {
		t.Fatalf("greeting = %q, want service-ready reply", greeting)
	}

	srv.shutdown()

	assertServeReturned(t, serveErr)
	assertServerSessionsClosed(t, srv)
	assertControlConnectionClosed(t, conn, reader)
}

func newTestServer(storage *filesystem.Backend, ln net.Listener) *server {
	return &server{
		cfg: config{
			addr:     "",
			root:     "",
			user:     "",
			pass:     "",
			pasvHost: "",
		},
		authenticator: static.NewStaticAuthenticator("", ""),
		backend:       storage,
		ln:            ln,
		log:           log.New(io.Discard, "", 0),
		wg:            sync.WaitGroup{},
		mu:            sync.Mutex{},
		sessions:      make(map[*session]struct{}),
		closing:       false,
	}
}

func assertServeReturned(t *testing.T, serveErr <-chan error) {
	t.Helper()

	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serve did not return after shutdown")
	}
}

func assertServerSessionsClosed(t *testing.T, srv *server) {
	t.Helper()

	waitDone := make(chan struct{})

	go func() {
		srv.wg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
	case <-time.After(time.Second):
		t.Fatal("active session did not exit after shutdown")
	}
}

func assertControlConnectionClosed(t *testing.T, conn net.Conn, reader *bufio.Reader) {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(time.Second))

	_, err := reader.ReadString('\n')
	if err == nil {
		t.Fatal("client control connection remained readable after shutdown")
	}
}
