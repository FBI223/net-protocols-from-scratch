// Command tftpd serves a folder over TFTP (RFC 1350).
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	tftp "github.com/FBI223/net-protocols-from-scratch/tftp"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:6969", "UDP address to listen on")
	root := flag.String("root", "", "folder to serve (required)")
	write := flag.Bool("write", false, "allow uploads of new files")
	maxTransfers := flag.Int("max", 100, "maximum number of simultaneous transfers")
	flag.Parse()

	if *root == "" {
		log.Print("tftpd: -root is required")
		flag.Usage()
		os.Exit(2)
	}

	// Cancel the context on Ctrl+C or SIGTERM so the server stops cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &tftp.Server{
		Root:         *root,
		AllowWrite:   *write,
		MaxTransfers: *maxTransfers,
		Logger:       slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}

	log.Printf("tftpd: serving %s on %s (writes %v)", *root, *addr, *write)
	if err := srv.ListenAndServe(ctx, *addr); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("tftpd: %v", err) // prints to stderr and exits with status 1
	}
}
