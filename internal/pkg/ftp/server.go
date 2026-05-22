package ftp

import (
	"bufio"
	"errors"
	"io"
	"log"
	"net"
	"strings"
	"sync"

	"goftp/internal/pkg/auth"
	"goftp/internal/pkg/backend"
)

// ServerConfig contains dependencies required by the FTP server.
type ServerConfig struct {
	PassiveHost   string
	Authenticator auth.Authenticator
	Backend       backend.Backend
	Listener      net.Listener
	Logger        *log.Logger
}

// Server accepts FTP control connections and owns their sessions.
type Server struct {
	pasvHost      string
	authenticator auth.Authenticator
	backend       backend.Backend
	ln            net.Listener
	log           *log.Logger
	wg            sync.WaitGroup
	mu            sync.Mutex
	sessions      map[*session]struct{}
	closing       bool
}

// NewServer creates an FTP server from injected listener, auth, and storage dependencies.
func NewServer(cfg ServerConfig) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}

	return &Server{
		pasvHost:      cfg.PassiveHost,
		authenticator: cfg.Authenticator,
		backend:       cfg.Backend,
		ln:            cfg.Listener,
		log:           logger,
		wg:            sync.WaitGroup{},
		mu:            sync.Mutex{},
		sessions:      make(map[*session]struct{}),
		closing:       false,
	}
}

// Serve accepts control connections until the listener is closed or an accept error occurs.
func (s *Server) Serve() error {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}

			return err
		}

		sess := s.newSession(conn)
		if !s.registerSession(sess) {
			sess.close()

			continue
		}

		s.wg.Go(func() {
			s.handle(sess)
		})
	}
}

// Shutdown closes the listener and all active control/data connections.
func (s *Server) Shutdown() {
	s.mu.Lock()
	s.closing = true

	sessions := make([]*session, 0, len(s.sessions))
	for sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.mu.Unlock()

	_ = s.ln.Close()

	for _, sess := range sessions {
		sess.close()
	}
}

// Wait blocks until all active sessions return.
func (s *Server) Wait() {
	s.wg.Wait()
}

func (s *Server) newSession(conn net.Conn) *session {
	return &session{
		mu:         sync.Mutex{},
		srv:        s,
		conn:       conn,
		reader:     bufio.NewReader(conn),
		writer:     bufio.NewWriter(conn),
		user:       "",
		loggedIn:   false,
		cwd:        "/",
		transfer:   "A",
		pasv:       nil,
		data:       nil,
		renameFrom: "",
	}
}

func (s *Server) registerSession(sess *session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closing {
		return false
	}

	if s.sessions == nil {
		s.sessions = make(map[*session]struct{})
	}

	s.sessions[sess] = struct{}{}

	return true
}

func (s *Server) unregisterSession(sess *session) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, sess)
}

func (s *Server) handle(sess *session) {
	defer func() {
		s.unregisterSession(sess)
		sess.close()
	}()

	remote := sess.conn.RemoteAddr().String()

	s.log.Printf("client connected: %s", remote)
	defer s.log.Printf("client disconnected: %s", remote)

	sess.reply(ReplyServiceReady, "Go FTP server ready")

	for {
		line, err := sess.reader.ReadString('\n')
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.log.Printf("read from %s: %v", remote, err)
			}

			return
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}

		cmd, arg := splitCommand(line)
		if !sess.loggedIn && !isPreLoginCommand(cmd) {
			sess.reply(ReplyNotLoggedIn, "Please login with USER and PASS")

			continue
		}

		if quit := sess.handleCommand(cmd, arg); quit {
			return
		}
	}
}
