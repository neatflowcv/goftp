package ftp

// Command is an FTP control connection command name.
//
// Commands are conventionally sent as uppercase ASCII tokens. See the IANA FTP
// Commands and Extensions registry for the current command and extension
// registry:
// https://www.iana.org/assignments/ftp-commands-extensions/ftp-commands-extensions.xhtml
type Command = string

const (
	CommandUser Command = "USER"
	CommandPass Command = "PASS"
	CommandQuit Command = "QUIT"

	CommandSystem Command = "SYST"
	CommandFeat   Command = "FEAT"
	CommandOpts   Command = "OPTS"
	CommandNoop   Command = "NOOP"

	CommandPrintWorkingDirectory  Command = "PWD"
	CommandXPrintWorkingDirectory Command = "XPWD"
	CommandChangeWorkingDirectory Command = "CWD"
	CommandChangeToParent         Command = "CDUP"

	CommandType            Command = "TYPE"
	CommandPassive         Command = "PASV"
	CommandExtendedPassive Command = "EPSV"

	CommandList     Command = "LIST"
	CommandNameList Command = "NLST"
	CommandRetrieve Command = "RETR"
	CommandStore    Command = "STOR"
	CommandDelete   Command = "DELE"

	CommandMakeDirectory  Command = "MKD"
	CommandXMakeDirectory Command = "XMKD"
	CommandRemoveDir      Command = "RMD"
	CommandXRemoveDir     Command = "XRMD"

	CommandSize         Command = "SIZE"
	CommandModifiedTime Command = "MDTM"
	CommandRenameFrom   Command = "RNFR"
	CommandRenameTo     Command = "RNTO"
)

// ReplyCode is a three-digit FTP control connection reply code.
//
// See RFC 959 section 4.2.1 for the base FTP reply codes. RFC 959 is still
// the Internet Standard for base FTP, but it has later updates/extensions.
// https://www.rfc-editor.org/rfc/rfc959#section-4.2.1
//
// See the IANA FTP Commands and Extensions registry for the current command
// and extension registry:
// https://www.iana.org/assignments/ftp-commands-extensions/ftp-commands-extensions.xhtml
//
// See RFC 2428 section 3 for EPSV and the 229 reply code:
// https://www.rfc-editor.org/rfc/rfc2428#section-3
//
// See RFC 3659 sections 3 and 4 for MDTM and SIZE:
// https://www.rfc-editor.org/rfc/rfc3659#section-3
// https://www.rfc-editor.org/rfc/rfc3659#section-4
type ReplyCode int

const (
	ReplyFileStatusOK                   ReplyCode = 150
	ReplyCommandOK                      ReplyCode = 200
	ReplySystemStatus                   ReplyCode = 211
	ReplyFileStatus                     ReplyCode = 213
	ReplySystemType                     ReplyCode = 215
	ReplyServiceReady                   ReplyCode = 220
	ReplyServiceClosing                 ReplyCode = 221
	ReplyClosingDataConnection          ReplyCode = 226
	ReplyEnteringPassiveMode            ReplyCode = 227
	ReplyEnteringExtendedPassiveMode    ReplyCode = 229
	ReplyUserLoggedIn                   ReplyCode = 230
	ReplyRequestedFileActionOK          ReplyCode = 250
	ReplyPathnameCreated                ReplyCode = 257
	ReplyUserNameOKNeedPassword         ReplyCode = 331
	ReplyRequestedFileActionPending     ReplyCode = 350
	ReplyCannotOpenDataConnection       ReplyCode = 425
	ReplyConnectionClosedTransferAbort  ReplyCode = 426
	ReplySyntaxErrorInParameters        ReplyCode = 501
	ReplyCommandNotImplemented          ReplyCode = 502
	ReplyBadCommandSequence             ReplyCode = 503
	ReplyCommandParameterNotImplemented ReplyCode = 504
	ReplyNotLoggedIn                    ReplyCode = 530
	ReplyRequestedActionNotTaken        ReplyCode = 550
)
