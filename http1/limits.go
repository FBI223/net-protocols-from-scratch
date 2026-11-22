package http1

import (
	"errors"
	"time"
)

// Limits bounds the resources one connection may consume. A zero field
// disables that check; start from DefaultLimits on public listeners.
type Limits struct {
	MaxRequestLineBytes int           // Exceeded: 414.
	MaxHeaderBytes      int           // Exceeded: 431.
	MaxHeaderCount      int           // Exceeded: 431. Many small headers cost more than few large ones.
	MaxBodyBytes        int64         // Exceeded: 413. Enforced for chunked bodies too.
	MaxChunkLineBytes   int           // Chunk-size line incl. ignored extensions.
	ReadHeaderTimeout   time.Duration // Slow-loris defence.
	ReadBodyTimeout     time.Duration // Whole body, counted from the end of the headers.
	IdleTimeout         time.Duration // Keep-alive wait for the next request.
	WriteTimeout        time.Duration // End of headers to end of response, handler included.
}

// DefaultLimits returns conservative limits for a public server.
func DefaultLimits() Limits {
	return Limits{
		MaxRequestLineBytes: 8 << 10, // RFC 9112 §3 minimum.
		MaxHeaderBytes:      16 << 10,
		MaxHeaderCount:      100,
		MaxBodyBytes:        1 << 20,
		MaxChunkLineBytes:   1 << 10,
		ReadHeaderTimeout:   10 * time.Second,
		ReadBodyTimeout:     30 * time.Second,
		IdleTimeout:         60 * time.Second,
		WriteTimeout:        30 * time.Second,
	}
}

// Validate rejects negative values and an inconsistent header budget.
func (l Limits) Validate() error {
	if l.MaxRequestLineBytes < 0 || l.MaxHeaderBytes < 0 || l.MaxHeaderCount < 0 ||
		l.MaxBodyBytes < 0 || l.MaxChunkLineBytes < 0 ||
		l.ReadHeaderTimeout < 0 || l.ReadBodyTimeout < 0 || l.IdleTimeout < 0 || l.WriteTimeout < 0 {
		return errors.New("http1: limits must not be negative")
	}
	// The request line is part of the header section.
	if l.MaxHeaderBytes != 0 && l.MaxRequestLineBytes > l.MaxHeaderBytes {
		return errors.New("http1: MaxRequestLineBytes exceeds MaxHeaderBytes")
	}
	return nil
}
