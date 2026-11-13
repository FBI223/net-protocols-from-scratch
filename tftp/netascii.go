package tftp

import (
	"io"
	"runtime"
)

// Netascii (RFC 1350 §5) is the text mode of TFTP: ASCII in which a line
// always ends with CR LF on the wire, whatever the two hosts use themselves.
// Each side converts between its own line end and the wire form, so neither
// has to know what the other runs. A CR that does not start a line end is
// sent as CR NUL, so it can be told apart from a line end.
//
// The rules, by the line end of the host:
//
//	LF host    file -> wire   LF -> CR LF, CR -> CR NUL
//	           wire -> file   CR LF -> LF, CR NUL -> CR
//	CR LF host file -> wire   CR LF -> CR LF, LF -> CR LF, CR -> CR NUL
//	           wire -> file   CR LF -> CR LF, CR NUL -> CR
//
// Bytes above 0x7F are passed through: real programs send UTF-8 and Latin-1
// in this mode, and refusing them would only make transfers fail. On the wire
// a CR followed by anything but LF or NUL breaks the RFC; it is kept as a CR
// rather than treated as an error, and a lone LF is kept as an LF.

// Transfer modes (RFC 1350 §5). "mail" is obsolete and not supported.
const (
	ModeOctet    = "octet"    // bytes are sent exactly as they are
	ModeNetascii = "netascii" // text; line ends travel as CR LF
)

const (
	asciiCR  = '\r'
	asciiLF  = '\n'
	asciiNUL = 0
)

// lineEnd is how a platform ends a line in a text file.
type lineEnd string

const (
	lineLF   lineEnd = "\n"   // Unix, macOS
	lineCRLF lineEnd = "\r\n" // Windows
)

// nativeLineEnd is the line end of the platform we run on. The conversion
// code takes the line end as a parameter, so tests can try both of them on
// any platform.
func nativeLineEnd() lineEnd {
	if runtime.GOOS == "windows" {
		return lineCRLF
	}
	return lineLF
}

// netasciiReader encodes a text file into netascii as it is read.
type netasciiReader struct {
	src  io.Reader
	crlf bool   // the host ends lines with CR LF
	in   []byte // scratch space for reading src
	buf  []byte // backing store for out
	out  []byte // encoded bytes not yet handed out
	cr   bool   // CR LF host: the last byte was a CR, the next one decides
	err  error  // error from src, reported once out is empty
}

// newNetasciiReader returns a reader that gives the netascii form of what r
// holds. The encoded stream is longer than r, because every line end grows.
func newNetasciiReader(r io.Reader, host lineEnd) io.Reader {
	return &netasciiReader{src: r, crlf: host == lineCRLF, in: make([]byte, 512)}
}

func (r *netasciiReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.out) == 0 {
		if r.err != nil {
			if r.cr && r.err == io.EOF {
				// A CR at the very end of the file is a lone CR.
				r.cr = false
				r.buf = append(r.buf[:0], asciiCR, asciiNUL)
				r.out = r.buf
				break
			}
			return 0, r.err
		}
		n, err := r.src.Read(r.in)
		r.buf = r.encode(r.buf[:0], r.in[:n])
		r.out = r.buf
		r.err = err
	}
	n := copy(p, r.out)
	r.out = r.out[n:]
	return n, nil
}

// encode appends the netascii form of in to out.
func (r *netasciiReader) encode(out, in []byte) []byte {
	for _, b := range in {
		if r.cr {
			// Only a CR LF host gets here: the byte after a CR decides.
			r.cr = false
			if b == asciiLF {
				out = append(out, asciiCR, asciiLF) // a line end, already in wire form
				continue
			}
			out = append(out, asciiCR, asciiNUL) // a lone CR; now look at b as usual
		}
		switch {
		case b == asciiLF:
			out = append(out, asciiCR, asciiLF)
		case b == asciiCR && r.crlf:
			r.cr = true
		case b == asciiCR:
			out = append(out, asciiCR, asciiNUL)
		default:
			out = append(out, b)
		}
	}
	return out
}

// netasciiWriter decodes netascii into a text file as it is written.
type netasciiWriter struct {
	dst  io.Writer
	crlf bool   // the host ends lines with CR LF
	cr   bool   // the last byte written was a CR, the next one decides
	buf  []byte // scratch space
}

// newNetasciiWriter returns a writer that decodes netascii and writes the
// text to w. Call Close after the last Write: it writes a CR that was still
// waiting for a byte that never came. Close does not close w.
func newNetasciiWriter(w io.Writer, host lineEnd) *netasciiWriter {
	return &netasciiWriter{dst: w, crlf: host == lineCRLF}
}

// Write decodes p. The CR that ends one block may belong to a line end that
// finishes in the next block, so the state is kept between calls.
func (w *netasciiWriter) Write(p []byte) (int, error) {
	out := w.buf[:0]
	for _, b := range p {
		if w.cr {
			w.cr = false
			switch b {
			case asciiLF:
				out = w.appendLineEnd(out)
				continue
			case asciiNUL:
				out = append(out, asciiCR)
				continue
			}
			out = append(out, asciiCR) // a CR that breaks the rules: keep it, then look at b
		}
		if b == asciiCR {
			w.cr = true
			continue
		}
		out = append(out, b)
	}
	w.buf = out
	if len(out) == 0 {
		return len(p), nil
	}
	if _, err := w.dst.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *netasciiWriter) appendLineEnd(out []byte) []byte {
	if w.crlf {
		return append(out, asciiCR, asciiLF)
	}
	return append(out, asciiLF)
}

// Close writes a CR that was still waiting for the byte after it.
func (w *netasciiWriter) Close() error {
	if !w.cr {
		return nil
	}
	w.cr = false
	_, err := w.dst.Write([]byte{asciiCR})
	return err
}

// readerFor returns what sendFile should read for a transfer in this mode.
func readerFor(mode string, r io.Reader) io.Reader {
	if mode == ModeNetascii {
		return newNetasciiReader(r, nativeLineEnd())
	}
	return r
}

// writerFor returns what recvFile should write to for a transfer in this
// mode, and a function to call after a successful transfer to write whatever
// the writer still holds. It does not close w.
func writerFor(mode string, w io.Writer) (io.Writer, func() error) {
	if mode == ModeNetascii {
		nw := newNetasciiWriter(w, nativeLineEnd())
		return nw, nw.Close
	}
	return w, func() error { return nil }
}

// supportedMode reports whether we can transfer in mode, given as the lower
// case string that Parse produces.
func supportedMode(mode string) bool {
	return mode == ModeOctet || mode == ModeNetascii
}
