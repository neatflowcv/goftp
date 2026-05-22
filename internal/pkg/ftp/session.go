package ftp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"goftp/internal/pkg/backend"
)

type session struct {
	mu         sync.Mutex
	srv        *Server
	conn       net.Conn
	reader     *bufio.Reader
	writer     *bufio.Writer
	user       string
	loggedIn   bool
	cwd        string
	transfer   string
	pasv       net.Listener
	data       net.Conn
	renameFrom string
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
	case CommandUser, CommandPass, CommandQuit, CommandSystem, CommandFeat, CommandNoop:
		return true
	default:
		return false
	}
}

func (s *session) handleCommand(cmd, arg string) bool { //nolint:cyclop,funlen
	switch cmd {
	case CommandUser:
		s.user = arg
		s.loggedIn = false
		s.reply(ReplyUserNameOKNeedPassword, "Password required")
	case CommandPass:
		if s.authOK(arg) {
			s.loggedIn = true
			s.reply(ReplyUserLoggedIn, "Login successful")
		} else {
			s.reply(ReplyNotLoggedIn, "Login incorrect")
		}
	case CommandSystem:
		s.reply(ReplySystemType, "UNIX Type: L8")
	case CommandFeat:
		s.replyLines(ReplySystemStatus, []string{
			"Features:",
			" EPSV",
			" PASV",
			" SIZE",
			" MDTM",
			" UTF8",
			"End",
		})
	case CommandOpts:
		if strings.EqualFold(arg, "UTF8 ON") {
			s.reply(ReplyCommandOK, "UTF8 enabled")
		} else {
			s.reply(ReplyCommandNotImplemented, "Option not implemented")
		}
	case CommandNoop:
		s.reply(ReplyCommandOK, "OK")
	case CommandPrintWorkingDirectory, CommandXPrintWorkingDirectory:
		s.reply(ReplyPathnameCreated, fmt.Sprintf("\"%s\" is the current directory", s.cwd))
	case CommandType:
		s.setType(arg)
	case CommandChangeWorkingDirectory:
		s.cwdCommand(arg)
	case CommandChangeToParent:
		s.cwdCommand("..")
	case CommandPassive:
		s.enterPassive(false)
	case CommandExtendedPassive:
		s.enterPassive(true)
	case CommandList:
		s.list(arg, true)
	case CommandNameList:
		s.list(arg, false)
	case CommandRetrieve:
		s.retrieve(arg)
	case CommandStore:
		s.store(arg)
	case CommandDelete:
		s.deleteFile(arg)
	case CommandMakeDirectory, CommandXMakeDirectory:
		s.makeDir(arg)
	case CommandRemoveDir, CommandXRemoveDir:
		s.removeDir(arg)
	case CommandSize:
		s.size(arg)
	case CommandModifiedTime:
		s.modifiedTime(arg)
	case CommandRenameFrom:
		s.renameFromCommand(arg)
	case CommandRenameTo:
		s.renameToCommand(arg)
	case CommandQuit:
		s.reply(ReplyServiceClosing, "Goodbye")

		return true
	default:
		s.reply(ReplyCommandNotImplemented, "Command not implemented")
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
		s.reply(ReplySyntaxErrorInParameters, "Missing type")

		return
	}

	typ := strings.ToUpper(fields[0])
	switch typ {
	case "A", "I":
		s.transfer = typ
		s.reply(ReplyCommandOK, "Type set")
	default:
		s.reply(ReplyCommandParameterNotImplemented, "Unsupported type")
	}
}

func (s *session) cwdCommand(arg string) {
	if arg == "" {
		arg = "/"
	}

	virt := s.cleanVirtual(arg)

	entry, err := s.srv.backend.Stat(context.Background(), virt)
	if err != nil {
		s.reply(ReplyRequestedActionNotTaken, err.Error())

		return
	}

	if !entry.IsDir() {
		s.reply(ReplyRequestedActionNotTaken, "Not a directory")

		return
	}

	s.cwd = virt
	s.reply(ReplyRequestedFileActionOK, "Directory changed")
}

func (s *session) enterPassive(epsv bool) {
	s.closePassive()

	var lc net.ListenConfig

	ln, err := lc.Listen(context.Background(), "tcp", ":0")
	if err != nil {
		s.reply(ReplyCannotOpenDataConnection, "Cannot open passive connection")

		return
	}

	s.setPassive(ln)

	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		s.reply(ReplyCannotOpenDataConnection, "Cannot open passive connection")
		s.closePassive()

		return
	}

	port := tcpAddr.Port
	if epsv {
		s.reply(ReplyEnteringExtendedPassiveMode, fmt.Sprintf("Entering Extended Passive Mode (|||%d|)", port))

		return
	}

	host := s.srv.pasvHost
	if host == "" {
		host = listenerHost(s.srv.ln.Addr(), s.conn.LocalAddr())
	}

	ip := net.ParseIP(host).To4()
	if ip == nil {
		s.reply(ReplyCannotOpenDataConnection, "PASV requires an IPv4 pasv-host")
		s.closePassive()

		return
	}

	p1 := port / 256
	p2 := port % 256
	s.reply(ReplyEnteringPassiveMode, fmt.Sprintf("Entering Passive Mode (%d,%d,%d,%d,%d,%d)", ip[0], ip[1], ip[2], ip[3], p1, p2)) //nolint:lll
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
		s.reply(ReplyRequestedActionNotTaken, err.Error())

		return
	}

	if !s.hasPassive() {
		return
	}

	s.reply(ReplyFileStatusOK, "Opening data connection")

	conn, ok := s.acceptData()
	if !ok {
		return
	}
	defer func() {
		s.clearData(conn)
		_ = conn.Close()
	}()

	err = s.writeListTarget(conn, virt, entry, long)
	if err != nil {
		s.reply(ReplyConnectionClosedTransferAbort, "Transfer aborted")

		return
	}

	s.reply(ReplyClosingDataConnection, "Transfer complete")
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
		s.reply(ReplySyntaxErrorInParameters, "Missing path")

		return
	}

	virt := s.cleanVirtual(arg)

	entry, err := s.srv.backend.Stat(context.Background(), virt)
	if err != nil {
		s.reply(ReplyRequestedActionNotTaken, err.Error())

		return
	}

	if entry.IsDir() {
		s.reply(ReplyRequestedActionNotTaken, "Not a file")

		return
	}

	reader, err := s.srv.backend.OpenReader(context.Background(), virt)
	if err != nil {
		s.reply(ReplyRequestedActionNotTaken, err.Error())

		return
	}
	defer func() {
		_ = reader.Close()
	}()

	if !s.hasPassive() {
		return
	}

	s.reply(ReplyFileStatusOK, "Opening data connection")

	conn, ok := s.acceptData()
	if !ok {
		return
	}
	defer func() {
		s.clearData(conn)
		_ = conn.Close()
	}()

	_, err = io.Copy(conn, reader)
	if err != nil {
		s.reply(ReplyConnectionClosedTransferAbort, "Transfer aborted")

		return
	}

	s.reply(ReplyClosingDataConnection, "Transfer complete")
}

func (s *session) store(arg string) {
	if arg == "" {
		s.reply(ReplySyntaxErrorInParameters, "Missing path")

		return
	}

	if !s.hasPassive() {
		return
	}

	virt := s.cleanVirtual(arg)

	writer, err := s.srv.backend.CreateWriter(context.Background(), virt)
	if err != nil {
		s.reply(ReplyRequestedActionNotTaken, err.Error())

		return
	}

	closeWriter := true
	defer func() {
		if closeWriter {
			_ = writer.Close()
		}
	}()

	s.reply(ReplyFileStatusOK, "Opening data connection")

	conn, ok := s.acceptData()
	if !ok {
		return
	}
	defer func() {
		s.clearData(conn)
		_ = conn.Close()
	}()

	_, err = io.Copy(writer, conn)
	if err != nil {
		s.reply(ReplyConnectionClosedTransferAbort, "Transfer aborted")

		return
	}

	closeWriter = false

	err = writer.Close()
	if err != nil {
		s.reply(ReplyConnectionClosedTransferAbort, "Transfer aborted")

		return
	}

	s.reply(ReplyClosingDataConnection, "Transfer complete")
}

func (s *session) deleteFile(arg string) {
	if arg == "" {
		s.reply(ReplySyntaxErrorInParameters, "Missing path")

		return
	}

	virt := s.cleanVirtual(arg)

	entry, err := s.srv.backend.Stat(context.Background(), virt)
	if err != nil {
		s.reply(ReplyRequestedActionNotTaken, err.Error())

		return
	}

	if entry.IsDir() {
		s.reply(ReplyRequestedActionNotTaken, "Use RMD for directories")

		return
	}

	err = s.srv.backend.DeleteFile(context.Background(), virt)
	if err != nil {
		s.reply(ReplyRequestedActionNotTaken, err.Error())

		return
	}

	s.reply(ReplyRequestedFileActionOK, "File deleted")
}

func (s *session) makeDir(arg string) {
	if arg == "" {
		s.reply(ReplySyntaxErrorInParameters, "Missing path")

		return
	}

	virt := s.cleanVirtual(arg)

	err := s.srv.backend.MakeDir(context.Background(), virt)
	if err != nil {
		s.reply(ReplyRequestedActionNotTaken, err.Error())

		return
	}

	s.reply(ReplyPathnameCreated, fmt.Sprintf("\"%s\" created", virt))
}

func (s *session) removeDir(arg string) {
	if arg == "" {
		s.reply(ReplySyntaxErrorInParameters, "Missing path")

		return
	}

	err := s.srv.backend.RemoveDir(context.Background(), s.cleanVirtual(arg))
	if err != nil {
		s.reply(ReplyRequestedActionNotTaken, err.Error())

		return
	}

	s.reply(ReplyRequestedFileActionOK, "Directory removed")
}

func (s *session) size(arg string) {
	if arg == "" {
		s.reply(ReplySyntaxErrorInParameters, "Missing path")

		return
	}

	entry, err := s.srv.backend.Stat(context.Background(), s.cleanVirtual(arg))
	if err != nil {
		s.reply(ReplyRequestedActionNotTaken, err.Error())

		return
	}

	if entry.IsDir() {
		s.reply(ReplyRequestedActionNotTaken, "Not a file")

		return
	}

	s.reply(ReplyFileStatus, strconv.FormatInt(entry.Size(), 10))
}

func (s *session) modifiedTime(arg string) {
	if arg == "" {
		s.reply(ReplySyntaxErrorInParameters, "Missing path")

		return
	}

	entry, err := s.srv.backend.Stat(context.Background(), s.cleanVirtual(arg))
	if err != nil {
		s.reply(ReplyRequestedActionNotTaken, err.Error())

		return
	}

	s.reply(ReplyFileStatus, entry.ModTime().UTC().Format("20060102150405"))
}

func (s *session) renameFromCommand(arg string) {
	if arg == "" {
		s.reply(ReplySyntaxErrorInParameters, "Missing path")

		return
	}

	virt := s.cleanVirtual(arg)

	_, err := s.srv.backend.Stat(context.Background(), virt)
	if err != nil {
		s.reply(ReplyRequestedActionNotTaken, err.Error())

		return
	}

	s.renameFrom = virt
	s.reply(ReplyRequestedFileActionPending, "Ready for RNTO")
}

func (s *session) renameToCommand(arg string) {
	if s.renameFrom == "" {
		s.reply(ReplyBadCommandSequence, "Use RNFR first")

		return
	}

	if arg == "" {
		s.reply(ReplySyntaxErrorInParameters, "Missing path")

		return
	}

	defer func() {
		s.renameFrom = ""
	}()

	err := s.srv.backend.Rename(context.Background(), s.renameFrom, s.cleanVirtual(arg))
	if err != nil {
		s.reply(ReplyRequestedActionNotTaken, err.Error())

		return
	}

	s.reply(ReplyRequestedFileActionOK, "Rename successful")
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
	ln := s.takePassive()
	if ln == nil {
		s.reply(ReplyCannotOpenDataConnection, "Use PASV or EPSV first")

		return nil, false
	}

	defer func() {
		_ = ln.Close()
	}()

	if tcp, ok := ln.(*net.TCPListener); ok {
		_ = tcp.SetDeadline(time.Now().Add(30 * time.Second))
	}

	conn, err := ln.Accept()
	if err != nil {
		s.reply(ReplyCannotOpenDataConnection, "Cannot open data connection")

		return nil, false
	}

	s.setData(conn)

	return conn, true
}

func (s *session) hasPassive() bool {
	s.mu.Lock()
	hasPassive := s.pasv != nil
	s.mu.Unlock()

	if !hasPassive {
		s.reply(ReplyCannotOpenDataConnection, "Use PASV or EPSV first")

		return false
	}

	return true
}

func (s *session) close() {
	if s.conn != nil {
		_ = s.conn.Close()
	}

	s.closePassive()
	s.closeData()
}

func (s *session) setPassive(ln net.Listener) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.pasv = ln
}

func (s *session) takePassive() net.Listener {
	s.mu.Lock()
	defer s.mu.Unlock()

	ln := s.pasv
	s.pasv = nil

	return ln
}

func (s *session) setData(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data = conn
}

func (s *session) clearData(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.data == conn {
		s.data = nil
	}
}

func (s *session) closePassive() {
	ln := s.takePassive()
	if ln != nil {
		_ = ln.Close()
	}
}

func (s *session) closeData() {
	s.mu.Lock()
	conn := s.data
	s.data = nil
	s.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
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

func (s *session) reply(code ReplyCode, msg string) {
	_, _ = fmt.Fprintf(s.writer, "%d %s\r\n", code, msg)
	_ = s.writer.Flush()
}

func (s *session) replyLines(code ReplyCode, lines []string) {
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
