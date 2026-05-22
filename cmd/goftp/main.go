package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"goftp/internal/pkg/auth/static"
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

	logger := log.New(os.Stdout, "goftp: ", log.LstdFlags)

	srv := ftp.NewServer(ftp.ServerConfig{
		PassiveHost:   cfg.pasvHost,
		Authenticator: static.NewStaticAuthenticator(cfg.user, cfg.pass),
		Backend:       storage,
		Listener:      ln,
		Logger:        logger,
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve()
	}()

	logger.Printf("listening on %s, root=%s", ln.Addr(), cfg.root)

	select {
	case <-ctx.Done():
		srv.Shutdown()
		srv.Wait()

		return nil
	case err := <-errCh:
		srv.Shutdown()
		srv.Wait()

		return err
	}
}
