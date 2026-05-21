package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"goftp/internal/pkg/ftp"
)

type config struct {
	addr     string
	root     string
	user     string
	pass     string
	pasvHost string
}

type server struct {
	cfg     config
	rootAbs string
	ln      net.Listener
	log     *log.Logger
	wg      sync.WaitGroup
}

type session struct {
	srv        *server
	conn       net.Conn
	reader     *bufio.Reader
	writer     *bufio.Writer
	user       string
	loggedIn   bool
	cwd        string
	transfer   string
	pasv       net.Listener
	renameFrom string
}

func main() {
	var cfg config
	flag.StringVar(&cfg.addr, "addr", "127.0.0.1:2121", "control address to listen on")
	flag.StringVar(&cfg.root, "root", ".", "directory exposed as FTP root")
	flag.StringVar(&cfg.user, "user", "anonymous", "username")
	flag.StringVar(&cfg.pass, "pass", "", "password; empty accepts any password")
	flag.StringVar(&cfg.pasvHost, "pasv-host", "", "host/IP advertised for passive transfers; defaults to control listener host")
	flag.Parse()

	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}

func run(cfg config) error {
	rootAbs, err := filepath.Abs(cfg.root)
	if err != nil {
		return err
	}
	rootAbs, err = filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}
	info, err := os.Stat(rootAbs)
	if err != nil {
		return fmt.Errorf("stat root: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("root is not a directory: %s", rootAbs)
	}

	ln, err := net.Listen("tcp", cfg.addr)
	if err != nil {
		return err
	}

	srv := &server{
		cfg:     cfg,
		rootAbs: rootAbs,
		ln:      ln,
		log:     log.New(os.Stdout, "goftp: ", log.LstdFlags),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.serve()
	}()

	srv.log.Printf("listening on %s, root=%s", ln.Addr(), rootAbs)

	select {
	case <-ctx.Done():
		_ = ln.Close()
		srv.wg.Wait()
		return nil
	case err := <-errCh:
		return err
	}
}

func (s *server) serve() error {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(conn)
		}()
	}
}

func (s *server) handle(conn net.Conn) {
	defer conn.Close()

	sess := &session{
		srv:      s,
		conn:     conn,
		reader:   bufio.NewReader(conn),
		writer:   bufio.NewWriter(conn),
		cwd:      "/",
		transfer: "A",
	}
	defer sess.closePassive()

	remote := conn.RemoteAddr().String()
	s.log.Printf("client connected: %s", remote)
	defer s.log.Printf("client disconnected: %s", remote)

	sess.reply(ftp.ReplyServiceReady, "Go FTP server ready")
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
			sess.reply(ftp.ReplyNotLoggedIn, "Please login with USER and PASS")
			continue
		}

		if quit := sess.handleCommand(cmd, arg); quit {
			return
		}
	}
}

func splitCommand(line string) (string, string) {
	cmd, arg, ok := strings.Cut(line, " ")
	if !ok {
		return strings.ToUpper(line), ""
	}
	return strings.ToUpper(cmd), strings.TrimSpace(arg)
}

func isPreLoginCommand(cmd string) bool {
	switch cmd {
	case "USER", "PASS", "QUIT", "SYST", "FEAT", "NOOP":
		return true
	default:
		return false
	}
}

func (s *session) handleCommand(cmd, arg string) bool {
	switch cmd {
	case "USER":
		s.user = arg
		s.loggedIn = false
		s.reply(ftp.ReplyUserNameOKNeedPassword, "Password required")
	case "PASS":
		if s.authOK(arg) {
			s.loggedIn = true
			s.reply(ftp.ReplyUserLoggedIn, "Login successful")
		} else {
			s.reply(ftp.ReplyNotLoggedIn, "Login incorrect")
		}
	case "SYST":
		s.reply(ftp.ReplySystemType, "UNIX Type: L8")
	case "FEAT":
		s.replyLines(ftp.ReplySystemStatus, []string{
			"Features:",
			" EPSV",
			" PASV",
			" SIZE",
			" MDTM",
			" UTF8",
			"End",
		})
	case "OPTS":
		if strings.EqualFold(arg, "UTF8 ON") {
			s.reply(ftp.ReplyCommandOK, "UTF8 enabled")
		} else {
			s.reply(ftp.ReplyCommandNotImplemented, "Option not implemented")
		}
	case "NOOP":
		s.reply(ftp.ReplyCommandOK, "OK")
	case "PWD", "XPWD":
		s.reply(ftp.ReplyPathnameCreated, fmt.Sprintf("\"%s\" is the current directory", s.cwd))
	case "TYPE":
		s.setType(arg)
	case "CWD":
		s.cwdCommand(arg)
	case "CDUP":
		s.cwdCommand("..")
	case "PASV":
		s.enterPassive(false)
	case "EPSV":
		s.enterPassive(true)
	case "LIST":
		s.list(arg, true)
	case "NLST":
		s.list(arg, false)
	case "RETR":
		s.retrieve(arg)
	case "STOR":
		s.store(arg)
	case "DELE":
		s.deleteFile(arg)
	case "MKD", "XMKD":
		s.makeDir(arg)
	case "RMD", "XRMD":
		s.removeDir(arg)
	case "SIZE":
		s.size(arg)
	case "MDTM":
		s.modifiedTime(arg)
	case "RNFR":
		s.renameFromCommand(arg)
	case "RNTO":
		s.renameToCommand(arg)
	case "QUIT":
		s.reply(ftp.ReplyServiceClosing, "Goodbye")
		return true
	default:
		s.reply(ftp.ReplyCommandNotImplemented, "Command not implemented")
	}
	return false
}

func (s *session) authOK(pass string) bool {
	if s.srv.cfg.user != "" && s.user != s.srv.cfg.user {
		return false
	}
	if s.srv.cfg.pass == "" {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(pass), []byte(s.srv.cfg.pass)) == 1
}

func (s *session) setType(arg string) {
	fields := strings.Fields(arg)
	if len(fields) == 0 {
		s.reply(ftp.ReplySyntaxErrorInParameters, "Missing type")
		return
	}
	typ := strings.ToUpper(fields[0])
	switch typ {
	case "A", "I":
		s.transfer = typ
		s.reply(ftp.ReplyCommandOK, "Type set")
	default:
		s.reply(ftp.ReplyCommandParameterNotImplemented, "Unsupported type")
	}
}

func (s *session) cwdCommand(arg string) {
	if arg == "" {
		arg = "/"
	}
	virt := s.cleanVirtual(arg)
	real, err := s.realPath(virt, true)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}
	info, err := os.Stat(real)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}
	if !info.IsDir() {
		s.reply(ftp.ReplyRequestedActionNotTaken, "Not a directory")
		return
	}
	s.cwd = virt
	s.reply(ftp.ReplyRequestedFileActionOK, "Directory changed")
}

func (s *session) enterPassive(epsv bool) {
	s.closePassive()

	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		s.reply(ftp.ReplyCannotOpenDataConnection, "Cannot open passive connection")
		return
	}
	s.pasv = ln

	tcpAddr := ln.Addr().(*net.TCPAddr)
	port := tcpAddr.Port
	if epsv {
		s.reply(ftp.ReplyEnteringExtendedPassiveMode, fmt.Sprintf("Entering Extended Passive Mode (|||%d|)", port))
		return
	}

	host := s.srv.cfg.pasvHost
	if host == "" {
		host = listenerHost(s.srv.ln.Addr(), s.conn.LocalAddr())
	}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		s.reply(ftp.ReplyCannotOpenDataConnection, "PASV requires an IPv4 pasv-host")
		s.closePassive()
		return
	}
	p1 := port / 256
	p2 := port % 256
	s.reply(ftp.ReplyEnteringPassiveMode, fmt.Sprintf("Entering Passive Mode (%d,%d,%d,%d,%d,%d)", ip[0], ip[1], ip[2], ip[3], p1, p2))
}

func listenerHost(listenerAddr, localAddr net.Addr) string {
	if tcp, ok := listenerAddr.(*net.TCPAddr); ok {
		if tcp.IP != nil && !tcp.IP.IsUnspecified() {
			return tcp.IP.String()
		}
	}
	if tcp, ok := localAddr.(*net.TCPAddr); ok {
		return tcp.IP.String()
	}
	return "127.0.0.1"
}

func (s *session) list(arg string, long bool) {
	target := strings.TrimSpace(arg)
	if strings.HasPrefix(target, "-") {
		fields := strings.Fields(target)
		if len(fields) > 1 {
			target = fields[len(fields)-1]
		} else {
			target = ""
		}
	}
	virt := s.cleanVirtual(target)
	real, err := s.realPath(virt, true)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}

	info, err := os.Stat(real)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}

	if !s.hasPassive() {
		return
	}

	s.reply(ftp.ReplyFileStatusOK, "Opening data connection")
	conn, ok := s.acceptData()
	if !ok {
		return
	}
	defer conn.Close()

	if info.IsDir() {
		err = s.writeDirList(conn, real, long)
	} else if long {
		_, err = fmt.Fprint(conn, formatListLine(info.Name(), info))
	} else {
		_, err = fmt.Fprintf(conn, "%s\r\n", info.Name())
	}
	if err != nil {
		s.reply(ftp.ReplyConnectionClosedTransferAbort, "Transfer aborted")
		return
	}
	s.reply(ftp.ReplyClosingDataConnection, "Transfer complete")
}

func (s *session) retrieve(arg string) {
	if arg == "" {
		s.reply(ftp.ReplySyntaxErrorInParameters, "Missing path")
		return
	}
	real, err := s.realPath(s.cleanVirtual(arg), true)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}
	file, err := os.Open(real)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}
	if info.IsDir() {
		s.reply(ftp.ReplyRequestedActionNotTaken, "Not a file")
		return
	}

	if !s.hasPassive() {
		return
	}

	s.reply(ftp.ReplyFileStatusOK, "Opening data connection")
	conn, ok := s.acceptData()
	if !ok {
		return
	}
	defer conn.Close()

	if _, err := io.Copy(conn, file); err != nil {
		s.reply(ftp.ReplyConnectionClosedTransferAbort, "Transfer aborted")
		return
	}
	s.reply(ftp.ReplyClosingDataConnection, "Transfer complete")
}

func (s *session) store(arg string) {
	if arg == "" {
		s.reply(ftp.ReplySyntaxErrorInParameters, "Missing path")
		return
	}
	if !s.hasPassive() {
		return
	}
	virt := s.cleanVirtual(arg)
	real, err := s.realPathForCreate(virt)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}

	file, err := os.OpenFile(real, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}
	defer file.Close()

	s.reply(ftp.ReplyFileStatusOK, "Opening data connection")
	conn, ok := s.acceptData()
	if !ok {
		return
	}
	defer conn.Close()

	if _, err := io.Copy(file, conn); err != nil {
		s.reply(ftp.ReplyConnectionClosedTransferAbort, "Transfer aborted")
		return
	}
	s.reply(ftp.ReplyClosingDataConnection, "Transfer complete")
}

func (s *session) deleteFile(arg string) {
	if arg == "" {
		s.reply(ftp.ReplySyntaxErrorInParameters, "Missing path")
		return
	}
	real, err := s.realPath(s.cleanVirtual(arg), true)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}
	info, err := os.Stat(real)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}
	if info.IsDir() {
		s.reply(ftp.ReplyRequestedActionNotTaken, "Use RMD for directories")
		return
	}
	if err := os.Remove(real); err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}
	s.reply(ftp.ReplyRequestedFileActionOK, "File deleted")
}

func (s *session) makeDir(arg string) {
	if arg == "" {
		s.reply(ftp.ReplySyntaxErrorInParameters, "Missing path")
		return
	}
	virt := s.cleanVirtual(arg)
	real, err := s.realPathForCreate(virt)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}
	if err := os.Mkdir(real, 0o755); err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}
	s.reply(ftp.ReplyPathnameCreated, fmt.Sprintf("\"%s\" created", virt))
}

func (s *session) removeDir(arg string) {
	if arg == "" {
		s.reply(ftp.ReplySyntaxErrorInParameters, "Missing path")
		return
	}
	real, err := s.realPath(s.cleanVirtual(arg), true)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}
	if err := os.Remove(real); err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}
	s.reply(ftp.ReplyRequestedFileActionOK, "Directory removed")
}

func (s *session) size(arg string) {
	if arg == "" {
		s.reply(ftp.ReplySyntaxErrorInParameters, "Missing path")
		return
	}
	real, err := s.realPath(s.cleanVirtual(arg), true)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}
	info, err := os.Stat(real)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}
	if info.IsDir() {
		s.reply(ftp.ReplyRequestedActionNotTaken, "Not a file")
		return
	}
	s.reply(ftp.ReplyFileStatus, strconv.FormatInt(info.Size(), 10))
}

func (s *session) modifiedTime(arg string) {
	if arg == "" {
		s.reply(ftp.ReplySyntaxErrorInParameters, "Missing path")
		return
	}
	real, err := s.realPath(s.cleanVirtual(arg), true)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}
	info, err := os.Stat(real)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}
	s.reply(ftp.ReplyFileStatus, info.ModTime().UTC().Format("20060102150405"))
}

func (s *session) renameFromCommand(arg string) {
	if arg == "" {
		s.reply(ftp.ReplySyntaxErrorInParameters, "Missing path")
		return
	}
	real, err := s.realPath(s.cleanVirtual(arg), true)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}
	if _, err := os.Stat(real); err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}
	s.renameFrom = real
	s.reply(ftp.ReplyRequestedFileActionPending, "Ready for RNTO")
}

func (s *session) renameToCommand(arg string) {
	if s.renameFrom == "" {
		s.reply(ftp.ReplyBadCommandSequence, "Use RNFR first")
		return
	}
	if arg == "" {
		s.reply(ftp.ReplySyntaxErrorInParameters, "Missing path")
		return
	}
	defer func() {
		s.renameFrom = ""
	}()

	real, err := s.realPathForCreate(s.cleanVirtual(arg))
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}
	if err := os.Rename(s.renameFrom, real); err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())
		return
	}
	s.reply(ftp.ReplyRequestedFileActionOK, "Rename successful")
}

func (s *session) writeDirList(w io.Writer, real string, long bool) error {
	entries, err := os.ReadDir(real)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if long {
			if _, err := fmt.Fprint(w, formatListLine(entry.Name(), info)); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(w, "%s\r\n", entry.Name()); err != nil {
				return err
			}
		}
	}
	return nil
}

func formatListLine(name string, info os.FileInfo) string {
	mode := info.Mode()
	fileType := "-"
	if mode.IsDir() {
		fileType = "d"
	} else if mode&os.ModeSymlink != 0 {
		fileType = "l"
	}
	perms := mode.Perm().String()[1:]
	mtime := info.ModTime().Format("Jan _2 15:04")
	return fmt.Sprintf("%s%s 1 owner group %12d %s %s\r\n", fileType, perms, info.Size(), mtime, name)
}

func (s *session) acceptData() (net.Conn, bool) {
	if s.pasv == nil {
		s.reply(ftp.ReplyCannotOpenDataConnection, "Use PASV or EPSV first")
		return nil, false
	}
	ln := s.pasv
	s.pasv = nil
	defer ln.Close()

	if tcp, ok := ln.(*net.TCPListener); ok {
		_ = tcp.SetDeadline(time.Now().Add(30 * time.Second))
	}
	conn, err := ln.Accept()
	if err != nil {
		s.reply(ftp.ReplyCannotOpenDataConnection, "Cannot open data connection")
		return nil, false
	}
	return conn, true
}

func (s *session) hasPassive() bool {
	if s.pasv == nil {
		s.reply(ftp.ReplyCannotOpenDataConnection, "Use PASV or EPSV first")
		return false
	}
	return true
}

func (s *session) closePassive() {
	if s.pasv != nil {
		_ = s.pasv.Close()
		s.pasv = nil
	}
}

func (s *session) cleanVirtual(arg string) string {
	if arg == "" {
		return s.cwd
	}
	if strings.HasPrefix(arg, "/") {
		return path.Clean("/" + strings.TrimPrefix(arg, "/"))
	}
	return path.Clean(path.Join(s.cwd, arg))
}

func (s *session) realPath(virt string, mustExist bool) (string, error) {
	cleanVirt := path.Clean("/" + strings.TrimPrefix(virt, "/"))
	real := filepath.Join(s.srv.rootAbs, filepath.FromSlash(strings.TrimPrefix(cleanVirt, "/")))
	real = filepath.Clean(real)
	if mustExist {
		eval, err := filepath.EvalSymlinks(real)
		if err != nil {
			return "", err
		}
		real = eval
	}
	if !insideRoot(s.srv.rootAbs, real) {
		return "", errors.New("path escapes FTP root")
	}
	return real, nil
}

func (s *session) realPathForCreate(virt string) (string, error) {
	cleanVirt := path.Clean("/" + strings.TrimPrefix(virt, "/"))
	parentVirt := path.Dir(cleanVirt)
	parentReal, err := s.realPath(parentVirt, true)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(parentReal)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("parent is not a directory")
	}
	real := filepath.Join(parentReal, path.Base(cleanVirt))
	real = filepath.Clean(real)
	if !insideRoot(s.srv.rootAbs, real) {
		return "", errors.New("path escapes FTP root")
	}
	return real, nil
}

func insideRoot(root, candidate string) bool {
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
		candidate = strings.ToLower(candidate)
	}
	if candidate == root {
		return true
	}
	sepRoot := strings.TrimRight(root, string(filepath.Separator)) + string(filepath.Separator)
	return strings.HasPrefix(candidate, sepRoot)
}

func (s *session) reply(code ftp.ReplyCode, msg string) {
	fmt.Fprintf(s.writer, "%d %s\r\n", code, msg)
	_ = s.writer.Flush()
}

func (s *session) replyLines(code ftp.ReplyCode, lines []string) {
	if len(lines) == 0 {
		s.reply(code, "")
		return
	}
	if len(lines) == 1 {
		s.reply(code, lines[0])
		return
	}
	fmt.Fprintf(s.writer, "%d-%s\r\n", code, lines[0])
	for _, line := range lines[1 : len(lines)-1] {
		fmt.Fprintf(s.writer, "%s\r\n", line)
	}
	fmt.Fprintf(s.writer, "%d %s\r\n", code, lines[len(lines)-1])
	_ = s.writer.Flush()
}
