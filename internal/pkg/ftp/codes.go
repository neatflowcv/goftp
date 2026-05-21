package ftp

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
