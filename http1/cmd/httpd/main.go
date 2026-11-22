// Command httpd is a demo server built on the http1 package. It serves a
// small JSON key-value API, a static directory, and a few diagnostic
// endpoints, over TCP, a Unix socket, or TLS.
//
//	httpd -addr :8080 -root ./public
//	httpd -unix /tmp/httpd.sock
//	httpd -addr :8443 -tls-self-signed
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/FBI223/net-protocols-from-scratch/http1"
)

func main() {
	var (
		addr       = flag.String("addr", ":8080", "TCP listen address")
		unixPath   = flag.String("unix", "", "listen on this Unix socket instead of TCP")
		certFile   = flag.String("tls-cert", "", "TLS certificate file (PEM); needs -tls-key")
		keyFile    = flag.String("tls-key", "", "TLS private key file (PEM)")
		selfSigned = flag.Bool("tls-self-signed", false, "serve TLS with a throwaway self-signed certificate")
		root       = flag.String("root", "", "directory to serve under /files/")
		listDirs   = flag.Bool("list", false, "show directory listings under /files/")

		maxBody    = flag.Int64("max-body", 1<<20, "maximum request body in bytes")
		maxConns   = flag.Int("max-conns", 0, "maximum concurrent connections (0 = unlimited)")
		readHeader = flag.Duration("read-header-timeout", 10*time.Second, "time allowed to read a request head")
		idle       = flag.Duration("idle-timeout", 60*time.Second, "keep-alive idle timeout")
		grace      = flag.Duration("shutdown-grace", 10*time.Second, "time allowed for in-flight requests on shutdown")
	)
	flag.Parse()

	lim := http1.DefaultLimits()
	lim.MaxBodyBytes = *maxBody
	lim.ReadHeaderTimeout = *readHeader
	lim.IdleTimeout = *idle
	if err := lim.Validate(); err != nil {
		log.Fatalf("limits: %v", err)
	}

	handler, cleanup, err := routes(*root, *listDirs)
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()

	srv := &http1.Server{
		Handler:  handler,
		Limits:   lim,
		MaxConns: *maxConns,
		ErrorLog: log.Printf,
	}

	l, desc, err := listen(*addr, *unixPath, *certFile, *keyFile, *selfSigned)
	if err != nil {
		log.Fatal(err)
	}

	// Serve until SIGINT/SIGTERM, then drain in-flight requests.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(l) }()
	log.Printf("listening on %s", desc)

	select {
	case err := <-serveErr:
		log.Fatalf("server: %v", err)
	case <-ctx.Done():
	}
	stop() // A second Ctrl+C now kills the process instead of being swallowed.

	log.Print("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), *grace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v; closing connections", err)
		srv.Close()
	}
	if err := <-serveErr; !errors.Is(err, http1.ErrServerClosed) {
		log.Printf("server: %v", err)
	}
}

// listen opens the listener the flags ask for and describes it for the log.
func listen(addr, unixPath, certFile, keyFile string, selfSigned bool) (net.Listener, string, error) {
	var (
		l    net.Listener
		desc string
		err  error
	)
	if unixPath != "" {
		l, err = listenUnix(unixPath)
		desc = "unix:" + unixPath
	} else {
		l, err = net.Listen("tcp", addr)
		desc = addr
	}
	if err != nil {
		return nil, "", err
	}

	if (certFile == "") != (keyFile == "") {
		l.Close()
		return nil, "", errors.New("-tls-cert and -tls-key must be used together")
	}
	if certFile == "" && !selfSigned {
		return l, desc, nil
	}

	var cert tls.Certificate
	if selfSigned {
		cert, err = selfSignedCert()
	} else {
		cert, err = tls.LoadX509KeyPair(certFile, keyFile)
	}
	if err != nil {
		l.Close()
		return nil, "", fmt.Errorf("tls: %w", err)
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"http/1.1"}, // This server does not speak HTTP/2.
	}
	return tls.NewListener(l, cfg), desc + " (TLS)", nil
}

// listenUnix removes a stale socket file left by a crashed run, but refuses
// to take over a socket that something is still listening on.
func listenUnix(path string) (net.Listener, error) {
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			c.Close()
			return nil, fmt.Errorf("%s is already in use", path)
		}
		os.Remove(path)
	}
	return net.Listen("unix", path)
}

func selfSignedCert() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(30 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

func routes(root string, listDirs bool) (h http1.Handler, cleanup func(), err error) {
	rt := &http1.Router{}
	cleanup = func() {}

	rt.HandleFunc("GET", "/", func(w *http1.ResponseWriter, r *http1.Request) {
		io.WriteString(w, "http1 demo server\n\n"+
			"  GET    /hello/{name}   greeting\n"+
			"  POST   /echo           echo the request body\n"+
			"  GET    /headers        the request as received\n"+
			"  GET    /stream         five chunks, 200 ms apart\n"+
			"  GET    /kv             list keys\n"+
			"  GET    /kv/{key}       read a JSON document\n"+
			"  PUT    /kv/{key}       write a JSON document\n"+
			"  DELETE /kv/{key}       delete it\n"+
			"  GET    /files/...      static files (with -root)\n")
	})

	rt.HandleFunc("GET", "/hello/{name}", func(w *http1.ResponseWriter, r *http1.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		fmt.Fprintf(w, "Hello, %s!\n", r.PathValue("name"))
	})

	// Echoes the request body, whether it arrives with Content-Length or
	// chunked. Parse and limit errors map to their proper status.
	rt.HandleFunc("POST", "/echo", func(w *http1.ResponseWriter, r *http1.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			status, ok := http1.StatusCode(err)
			if !ok {
				status = 400
			}
			http1.Error(w, status, http1.StatusText(status))
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(body)
	})

	rt.HandleFunc("GET", "/headers", func(w *http1.ResponseWriter, r *http1.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		names := make([]string, 0, len(r.Header))
		for name := range r.Header {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Fprintf(w, "%s %s HTTP/%d.%d from %s\n", r.Method, r.Path, r.Major, r.Minor, r.RemoteAddr)
		for _, name := range names {
			for _, v := range r.Header[name] {
				fmt.Fprintf(w, "%s: %s\n", name, v)
			}
		}
	})

	// Flush after each line, so the client receives it as a chunk right away.
	rt.HandleFunc("GET", "/stream", func(w *http1.ResponseWriter, r *http1.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		for i := 1; i <= 5; i++ {
			fmt.Fprintf(w, "tick %d\n", i)
			if err := w.Flush(); err != nil {
				return // Client went away.
			}
			time.Sleep(200 * time.Millisecond)
		}
	})

	mountKV(rt, newStore())

	if root != "" {
		fs, err := http1.NewFileServer(root)
		if err != nil {
			return nil, nil, err
		}
		fs.ListDirs = listDirs
		cleanup = func() { fs.Close() }
		rt.Handle("GET", "/files/{path...}", fs)
	}
	return rt, cleanup, nil
}
