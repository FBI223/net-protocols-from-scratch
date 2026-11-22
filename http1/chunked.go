package http1

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
)

// chunkedBody decodes the chunked transfer coding (RFC 9112 §7.1):
//
//	chunk-size [; extension] CRLF  chunk-data CRLF  ...  0 CRLF  trailers CRLF
//
// It never reads past the final empty line, so the underlying reader stays
// positioned at the next request. After the first error every Read returns
// that same error.
type chunkedBody struct {
	r         *bufio.Reader
	lim       Limits
	remaining int64 // Bytes left in the current chunk.
	total     int64 // Sum of all chunk sizes announced so far.
	err       error // Sticky; io.EOF once the body is complete.
}

// Read implements io.Reader.
func (c *chunkedBody) Read(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	n, err := c.read(p)
	c.err = err
	return n, err
}

func (c *chunkedBody) read(p []byte) (int, error) {
	if c.remaining == 0 {
		size, err := c.readChunkHeader()
		if err != nil {
			return 0, err
		}
		if size == 0 {
			if err := c.readTrailers(); err != nil {
				return 0, err
			}
			return 0, io.EOF
		}
		c.remaining = size
	}

	if int64(len(p)) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.r.Read(p)
	c.remaining -= int64(n)
	if err == io.EOF {
		err = ErrTruncatedBody
	}
	if err == nil && c.remaining == 0 {
		err = c.readChunkEnd()
	}
	return n, err
}

// readChunkHeader reads a chunk-size line and returns the chunk size. The
// total size is checked here, before any data is read, so an oversized body
// is rejected as early as a Content-Length one.
func (c *chunkedBody) readChunkHeader() (int64, error) {
	line, err := readLine(c.r, c.lim.MaxChunkLineBytes)
	if err != nil {
		return 0, chunkLineError(err, ErrChunkLineTooLong)
	}

	// Extensions are ignored, but must not hide control characters.
	sizePart, ext, hasExt := bytes.Cut(line, []byte{';'})
	if hasExt && !isFieldValue(ext) {
		return 0, ErrMalformedChunk
	}
	sizePart = bytes.TrimRight(sizePart, " \t") // BWS before ';'.

	if len(sizePart) == 0 {
		return 0, ErrInvalidChunkSize
	}
	// Check digits by hand: ParseInt would also accept a sign.
	for _, ch := range sizePart {
		if !isHexDigit(ch) {
			return 0, ErrInvalidChunkSize
		}
	}
	size, err := strconv.ParseInt(string(sizePart), 16, 64)
	if err != nil {
		return 0, ErrInvalidChunkSize
	}

	if c.lim.MaxBodyBytes > 0 && size > c.lim.MaxBodyBytes-c.total {
		return 0, ErrBodyTooLarge
	}
	c.total += size
	return size, nil
}

// readChunkEnd consumes the CRLF that follows chunk data.
func (c *chunkedBody) readChunkEnd() error {
	var crlf [2]byte
	if _, err := io.ReadFull(c.r, crlf[:]); err != nil {
		if err == io.EOF || errors.Is(err, io.ErrUnexpectedEOF) {
			return ErrTruncatedBody
		}
		return err
	}
	if string(crlf[:]) != "\r\n" {
		return ErrMalformedChunk
	}
	return nil
}

// readTrailers consumes the trailer section up to the final empty line.
// Trailers are validated like headers, subject to the same limits, and then
// discarded: nothing here may influence how the request is interpreted.
func (c *chunkedBody) readTrailers() error {
	used, count := 0, 0
	for {
		max := 0
		if c.lim.MaxHeaderBytes > 0 {
			if max = c.lim.MaxHeaderBytes - used; max <= 0 {
				return ErrHeaderTooLarge
			}
		}
		line, err := readLine(c.r, max)
		if err != nil {
			return chunkLineError(err, ErrHeaderTooLarge)
		}
		used += len(line) + 2
		if len(line) == 0 {
			return nil
		}
		if count++; c.lim.MaxHeaderCount > 0 && count > c.lim.MaxHeaderCount {
			return ErrTooManyHeaders
		}
		if _, _, err := parseHeaderLine(line); err != nil {
			return err
		}
	}
}

// chunkLineError translates readLine failures inside a chunked body: running
// out of input mid-body means truncation, not a clean end of connection.
func chunkLineError(err, tooLong error) error {
	switch err {
	case errLineTooLong:
		return tooLong
	case io.EOF, io.ErrUnexpectedEOF:
		return ErrTruncatedBody
	}
	return err
}

func isHexDigit(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}
