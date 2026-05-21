# goftp

A small passive-mode FTP server written with the Go standard library.

## Run

```sh
go run ./cmd/goftp -addr 127.0.0.1:2121 -root ./data -user alice -pass secret
```

Create the root directory first if it does not exist:

```sh
mkdir -p data
```

## Connect

```sh
ftp 127.0.0.1 2121
```

Supported commands include `USER`, `PASS`, `PWD`, `CWD`, `CDUP`, `PASV`,
`EPSV`, `LIST`, `NLST`, `RETR`, `STOR`, `DELE`, `MKD`, `RMD`, `SIZE`,
`MDTM`, `RNFR`, `RNTO`, `TYPE`, `SYST`, `FEAT`, `NOOP`, and `QUIT`.

## Options

```text
-addr       control address to listen on, default 127.0.0.1:2121
-root       directory exposed as FTP root, default .
-user       username, default anonymous
-pass       password; empty accepts any password
-pasv-host  IPv4 address advertised for PASV when clients connect remotely
```

For remote clients behind NAT or a firewall, set `-pasv-host` to the address
the client can reach and allow the ephemeral passive data ports through the
firewall.
