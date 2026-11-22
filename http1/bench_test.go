package http1

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"testing"
)

// Run with: go test -run xxx -bench . -benchmem

const benchHead = "GET /search?q=http&page=2 HTTP/1.1\r\n" +
	"Host: example.com\r\n" +
	"User-Agent: Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/120.0 Safari/537.36\r\n" +
	"Accept: text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8\r\n" +
	"Accept-Language: en-US,en;q=0.9\r\n" +
	"Accept-Encoding: gzip, deflate, br\r\n" +
	"Cookie: session=abcdef0123456789; theme=dark\r\n" +
	"Connection: keep-alive\r\n\r\n"

// Parsing only: ours against net/http on the same bytes.
func BenchmarkParseRequest(b *testing.B) {
	data := []byte(benchHead)
	b.Run("http1", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		b.ReportAllocs()
		rd := bytes.NewReader(data)
		br := bufio.NewReader(rd)
		lim := DefaultLimits()
		for i := 0; i < b.N; i++ {
			rd.Reset(data)
			br.Reset(rd)
			if _, err := ReadRequest(br, lim); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("net/http", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		b.ReportAllocs()
		rd := bytes.NewReader(data)
		br := bufio.NewReader(rd)
		for i := 0; i < b.N; i++ {
			rd.Reset(data)
			br.Reset(rd)
			if _, err := http.ReadRequest(br); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkChunkedDecode(b *testing.B) {
	var enc bytes.Buffer
	for i := 0; i < 64; i++ {
		enc.WriteString("400\r\n")
		enc.Write(bytes.Repeat([]byte{'x'}, 1024))
		enc.WriteString("\r\n")
	}
	enc.WriteString("0\r\n\r\n")
	data := enc.Bytes()
	b.SetBytes(64 * 1024)
	b.ReportAllocs()
	rd := bytes.NewReader(data)
	br := bufio.NewReader(rd)
	for i := 0; i < b.N; i++ {
		rd.Reset(data)
		br.Reset(rd)
		if _, err := io.Copy(io.Discard, &chunkedBody{r: br, lim: Limits{}}); err != nil {
			b.Fatal(err)
		}
	}
}

// Whole server, keep-alive connections, many parallel clients, tiny response.
func BenchmarkServerHello(b *testing.B) {
	b.Run("http1", func(b *testing.B) {
		l, _ := net.Listen("tcp", "127.0.0.1:0")
		srv := &Server{Handler: HandlerFunc(func(w *ResponseWriter, r *Request) {
			w.Write([]byte("hello"))
		})}
		go srv.Serve(l)
		defer srv.Close()
		benchClient(b, "http://"+l.Addr().String())
	})
	b.Run("net/http", func(b *testing.B) {
		l, _ := net.Listen("tcp", "127.0.0.1:0")
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("hello"))
		})}
		go srv.Serve(l)
		defer srv.Close()
		benchClient(b, "http://"+l.Addr().String())
	})
}

func benchClient(b *testing.B, url string) {
	tr := &http.Transport{MaxIdleConnsPerHost: 64}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}
	b.ReportAllocs()
	b.SetParallelism(8)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp, err := client.Get(url)
			if err != nil {
				b.Error(err)
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	})
}
