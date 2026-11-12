// Command tftp downloads or uploads one file over TFTP (RFC 1350, octet mode).
//
//	tftp -addr 127.0.0.1:6969 get REMOTE [LOCAL]
//	tftp -addr 127.0.0.1:6969 put LOCAL [REMOTE]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	tftp "github.com/FBI223/net-protocols-from-scratch/tftp"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:6969", "server address host:port")
	flag.Usage = func() {
		_, _ = fmt.Fprintln(os.Stderr, "usage: tftp [-addr host:port] get REMOTE [LOCAL]")
		_, _ = fmt.Fprintln(os.Stderr, "       tftp [-addr host:port] put LOCAL [REMOTE]")
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()
	if len(args) < 2 || len(args) > 3 {
		flag.Usage()
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch args[0] {
	case "get":
		remote := args[1]
		local := filepath.Base(remote)
		if len(args) == 3 {
			local = args[2]
		}
		err = get(ctx, *addr, remote, local)
	case "put":
		local := args[1]
		remote := filepath.Base(local)
		if len(args) == 3 {
			remote = args[2]
		}
		err = put(ctx, *addr, local, remote)
	default:
		flag.Usage()
		os.Exit(2)
	}
	if err != nil {
		log.Fatalf("tftp: %v", err) // prints to stderr and exits with status 1
	}
}

// get downloads remote into a new local file; an existing file is never
// overwritten and a failed download leaves no partial file behind.
func get(ctx context.Context, addr, remote, local string) error {
	f, err := os.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if err := tftp.Get(ctx, addr, remote, f); err != nil {
		_ = f.Close()
		_ = os.Remove(local)
		return err
	}
	return f.Close()
}

func put(ctx context.Context, addr, local, remote string) error {
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return tftp.Put(ctx, addr, remote, f)
}
