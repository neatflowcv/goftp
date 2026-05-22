package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"goftp/internal/pkg/auth"
	"goftp/internal/pkg/auth/static"
	"goftp/internal/pkg/backend"
	"goftp/internal/pkg/backend/filesystem"
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
	cfg           config
	authenticator auth.Authenticator
	backend       backend.Backend
	ln            net.Listener
	log           *log.Logger
	wg            sync.WaitGroup
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
	flag.StringVar(&cfg.pasvHost, "pasv-host", "", "host/IP advertised for passive transfers; defaults to control listener host") //nolint:lll
	flag.Parse()

	err := run(cfg)
	if err != nil {
		log.Fatal(err)
	}
}

func run(cfg config) error {
	storage, err := filesystem.NewFilesystemBackend(cfg.root)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var lc net.ListenConfig

	ln, err := lc.Listen(ctx, "tcp", cfg.addr)
	if err != nil {
		return err
	}

	srv := &server{
		cfg:           cfg,
		authenticator: static.NewStaticAuthenticator(cfg.user, cfg.pass),
		backend:       storage,
		ln:            ln,
		log:           log.New(os.Stdout, "goftp: ", log.LstdFlags),
		wg:            sync.WaitGroup{},
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.serve()
	}()

	srv.log.Printf("listening on %s, root=%s", ln.Addr(), cfg.root)

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

		s.wg.Go(func() {
			s.handle(conn)
		})
	}
}

func (s *server) handle(conn net.Conn) {
	defer func() {
		_ = conn.Close()
	}()

	sess := &session{
		srv:        s,
		conn:       conn,
		reader:     bufio.NewReader(conn),
		writer:     bufio.NewWriter(conn),
		user:       "",
		loggedIn:   false,
		cwd:        "/",
		transfer:   "A",
		pasv:       nil,
		renameFrom: "",
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
	case ftp.CommandUser, ftp.CommandPass, ftp.CommandQuit, ftp.CommandSystem, ftp.CommandFeat, ftp.CommandNoop:
		return true
	default:
		return false
	}
}

func (s *session) handleCommand(cmd, arg string) bool { //nolint:cyclop,funlen
	switch cmd {
	case ftp.CommandUser:
		s.user = arg
		s.loggedIn = false
		s.reply(ftp.ReplyUserNameOKNeedPassword, "Password required")
	case ftp.CommandPass:
		if s.authOK(arg) {
			s.loggedIn = true
			s.reply(ftp.ReplyUserLoggedIn, "Login successful")
		} else {
			s.reply(ftp.ReplyNotLoggedIn, "Login incorrect")
		}
	case ftp.CommandSystem:
		s.reply(ftp.ReplySystemType, "UNIX Type: L8")
	case ftp.CommandFeat:
		s.replyLines(ftp.ReplySystemStatus, []string{
			"Features:",
			" EPSV",
			" PASV",
			" SIZE",
			" MDTM",
			" UTF8",
			"End",
		})
	case ftp.CommandOpts:
		if strings.EqualFold(arg, "UTF8 ON") {
			s.reply(ftp.ReplyCommandOK, "UTF8 enabled")
		} else {
			s.reply(ftp.ReplyCommandNotImplemented, "Option not implemented")
		}
	case ftp.CommandNoop:
		s.reply(ftp.ReplyCommandOK, "OK")
	case ftp.CommandPrintWorkingDirectory, ftp.CommandXPrintWorkingDirectory:
		s.reply(ftp.ReplyPathnameCreated, fmt.Sprintf("\"%s\" is the current directory", s.cwd))
	case ftp.CommandType:
		s.setType(arg)
	case ftp.CommandChangeWorkingDirectory:
		s.cwdCommand(arg)
	case ftp.CommandChangeToParent:
		s.cwdCommand("..")
	case ftp.CommandPassive:
		s.enterPassive(false)
	case ftp.CommandExtendedPassive:
		s.enterPassive(true)
	case ftp.CommandList:
		s.list(arg, true)
	case ftp.CommandNameList:
		s.list(arg, false)
	case ftp.CommandRetrieve:
		s.retrieve(arg)
	case ftp.CommandStore:
		s.store(arg)
	case ftp.CommandDelete:
		s.deleteFile(arg)
	case ftp.CommandMakeDirectory, ftp.CommandXMakeDirectory:
		s.makeDir(arg)
	case ftp.CommandRemoveDir, ftp.CommandXRemoveDir:
		s.removeDir(arg)
	case ftp.CommandSize:
		s.size(arg)
	case ftp.CommandModifiedTime:
		s.modifiedTime(arg)
	case ftp.CommandRenameFrom:
		s.renameFromCommand(arg)
	case ftp.CommandRenameTo:
		s.renameToCommand(arg)
	case ftp.CommandQuit:
		s.reply(ftp.ReplyServiceClosing, "Goodbye")

		return true
	default:
		s.reply(ftp.ReplyCommandNotImplemented, "Command not implemented")
	}

	return false
}

func (s *session) authOK(pass string) bool {
	if s.srv.authenticator == nil {
		return false
	}

	return s.srv.authenticator.Authenticate(s.user, pass)
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

	entry, err := s.srv.backend.Stat(context.Background(), virt)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())

		return
	}

	if !entry.IsDir() {
		s.reply(ftp.ReplyRequestedActionNotTaken, "Not a directory")

		return
	}

	s.cwd = virt
	s.reply(ftp.ReplyRequestedFileActionOK, "Directory changed")
}

func (s *session) enterPassive(epsv bool) {
	s.closePassive()

	var lc net.ListenConfig

	ln, err := lc.Listen(context.Background(), "tcp", ":0")
	if err != nil {
		s.reply(ftp.ReplyCannotOpenDataConnection, "Cannot open passive connection")

		return
	}

	s.pasv = ln

	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		s.reply(ftp.ReplyCannotOpenDataConnection, "Cannot open passive connection")
		s.closePassive()

		return
	}

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
	s.reply(ftp.ReplyEnteringPassiveMode, fmt.Sprintf("Entering Passive Mode (%d,%d,%d,%d,%d,%d)", ip[0], ip[1], ip[2], ip[3], p1, p2)) //nolint:lll
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

	entry, err := s.srv.backend.Stat(context.Background(), virt)
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
	defer func() {
		_ = conn.Close()
	}()

	err = s.writeListTarget(conn, virt, entry, long)
	if err != nil {
		s.reply(ftp.ReplyConnectionClosedTransferAbort, "Transfer aborted")

		return
	}

	s.reply(ftp.ReplyClosingDataConnection, "Transfer complete")
}

func (s *session) writeListTarget(w io.Writer, virtualPath string, entry *backend.Entry, long bool) error {
	switch {
	case entry.IsDir():
		return s.writeDirList(w, virtualPath, long)
	case long:
		_, err := fmt.Fprint(w, formatListLine(entry.Name(), entry))

		return err
	default:
		_, err := fmt.Fprintf(w, "%s\r\n", entry.Name())

		return err
	}
}

func (s *session) retrieve(arg string) {
	if arg == "" {
		s.reply(ftp.ReplySyntaxErrorInParameters, "Missing path")

		return
	}

	virt := s.cleanVirtual(arg)

	entry, err := s.srv.backend.Stat(context.Background(), virt)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())

		return
	}

	if entry.IsDir() {
		s.reply(ftp.ReplyRequestedActionNotTaken, "Not a file")

		return
	}

	reader, err := s.srv.backend.OpenReader(context.Background(), virt)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())

		return
	}
	defer func() {
		_ = reader.Close()
	}()

	if !s.hasPassive() {
		return
	}

	s.reply(ftp.ReplyFileStatusOK, "Opening data connection")

	conn, ok := s.acceptData()
	if !ok {
		return
	}
	defer func() {
		_ = conn.Close()
	}()

	_, err = io.Copy(conn, reader)
	if err != nil {
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

	writer, err := s.srv.backend.CreateWriter(context.Background(), virt)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())

		return
	}

	closeWriter := true
	defer func() {
		if closeWriter {
			_ = writer.Close()
		}
	}()

	s.reply(ftp.ReplyFileStatusOK, "Opening data connection")

	conn, ok := s.acceptData()
	if !ok {
		return
	}
	defer func() {
		_ = conn.Close()
	}()

	_, err = io.Copy(writer, conn)
	if err != nil {
		s.reply(ftp.ReplyConnectionClosedTransferAbort, "Transfer aborted")

		return
	}

	closeWriter = false

	err = writer.Close()
	if err != nil {
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

	virt := s.cleanVirtual(arg)

	entry, err := s.srv.backend.Stat(context.Background(), virt)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())

		return
	}

	if entry.IsDir() {
		s.reply(ftp.ReplyRequestedActionNotTaken, "Use RMD for directories")

		return
	}

	err = s.srv.backend.DeleteFile(context.Background(), virt)
	if err != nil {
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

	err := s.srv.backend.MakeDir(context.Background(), virt)
	if err != nil {
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

	err := s.srv.backend.RemoveDir(context.Background(), s.cleanVirtual(arg))
	if err != nil {
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

	entry, err := s.srv.backend.Stat(context.Background(), s.cleanVirtual(arg))
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())

		return
	}

	if entry.IsDir() {
		s.reply(ftp.ReplyRequestedActionNotTaken, "Not a file")

		return
	}

	s.reply(ftp.ReplyFileStatus, strconv.FormatInt(entry.Size(), 10))
}

func (s *session) modifiedTime(arg string) {
	if arg == "" {
		s.reply(ftp.ReplySyntaxErrorInParameters, "Missing path")

		return
	}

	entry, err := s.srv.backend.Stat(context.Background(), s.cleanVirtual(arg))
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())

		return
	}

	s.reply(ftp.ReplyFileStatus, entry.ModTime().UTC().Format("20060102150405"))
}

func (s *session) renameFromCommand(arg string) {
	if arg == "" {
		s.reply(ftp.ReplySyntaxErrorInParameters, "Missing path")

		return
	}

	virt := s.cleanVirtual(arg)

	_, err := s.srv.backend.Stat(context.Background(), virt)
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())

		return
	}

	s.renameFrom = virt
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

	err := s.srv.backend.Rename(context.Background(), s.renameFrom, s.cleanVirtual(arg))
	if err != nil {
		s.reply(ftp.ReplyRequestedActionNotTaken, err.Error())

		return
	}

	s.reply(ftp.ReplyRequestedFileActionOK, "Rename successful")
}

func (s *session) writeDirList(w io.Writer, virtualPath string, long bool) error {
	entries, err := s.srv.backend.List(context.Background(), virtualPath)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if long {
			_, err = fmt.Fprint(w, formatListLine(entry.Name(), entry))
			if err != nil {
				return err
			}
		} else {
			_, err = fmt.Fprintf(w, "%s\r\n", entry.Name())
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func formatListLine(name string, entry *backend.Entry) string {
	fileType := "-"
	perms := "rw-r--r--"

	if entry.IsDir() {
		fileType = "d"
		perms = "rwxr-xr-x"
	}

	mtime := entry.ModTime().Format("Jan _2 15:04")

	return fmt.Sprintf("%s%s 1 owner group %12d %s %s\r\n", fileType, perms, entry.Size(), mtime, name)
}

func (s *session) acceptData() (net.Conn, bool) {
	if s.pasv == nil {
		s.reply(ftp.ReplyCannotOpenDataConnection, "Use PASV or EPSV first")

		return nil, false
	}

	ln := s.pasv
	s.pasv = nil

	defer func() {
		_ = ln.Close()
	}()

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

	if after, ok := strings.CutPrefix(arg, "/"); ok {
		return path.Clean("/" + after)
	}

	return path.Clean(path.Join(s.cwd, arg))
}

func (s *session) reply(code ftp.ReplyCode, msg string) {
	_, _ = fmt.Fprintf(s.writer, "%d %s\r\n", code, msg)
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

	_, _ = fmt.Fprintf(s.writer, "%d-%s\r\n", code, lines[0])

	for _, line := range lines[1 : len(lines)-1] {
		_, _ = fmt.Fprintf(s.writer, "%s\r\n", line)
	}

	_, _ = fmt.Fprintf(s.writer, "%d %s\r\n", code, lines[len(lines)-1])
	_ = s.writer.Flush()
}
