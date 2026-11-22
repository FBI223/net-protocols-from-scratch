package http1

import (
	"html"
	"io"
	"mime"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// FileServer serves the files below one directory.
//
// The directory is opened as an os.Root, so the kernel-checked rules of that
// type apply: names cannot escape it through "..", and symbolic links that
// point outside are refused. Both cases look like a missing file.
//
// Supported: GET and HEAD, Content-Type from the file extension, Last-Modified
// and a weak ETag with If-None-Match / If-Modified-Since (304), a single
// byte range with 206 and 416, index.html for directories, and an optional
// directory listing. Multiple ranges are not supported: the Range header is
// then ignored and the whole file is sent, which RFC 9110 §14.2 allows.
type FileServer struct {
	// ListDirs enables an HTML listing for directories without index.html.
	// Without it such directories are 404.
	ListDirs bool

	root *os.Root
}

// NewFileServer opens dir for serving.
func NewFileServer(dir string) (*FileServer, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &FileServer{root: root}, nil
}

// Close releases the directory handle.
func (fs *FileServer) Close() error { return fs.root.Close() }

// ServeHTTP serves r.PathValue("path"), so mount it on a pattern ending in
// a {path...} wildcard, for example "/static/{path...}".
func (fs *FileServer) ServeHTTP(w *ResponseWriter, r *Request) {
	fs.ServeFile(w, r, r.PathValue("path"))
}

// ServeFile serves the file or directory called name, a slash-separated path
// relative to the root. An empty name is the root itself.
func (fs *FileServer) ServeFile(w *ResponseWriter, r *Request, name string) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		Error(w, 405, "405 method not allowed")
		return
	}

	// A trailing slash is only a hint that a directory is wanted. It is
	// stripped before opening because os.Root in Go 1.24 skips its escape
	// check for a symlink opened with a trailing slash ("link/" opened the
	// directory the link points to, even outside the root). Names with empty
	// or dot segments are refused outright for the same reason.
	trailingSlash := strings.HasSuffix(name, "/")
	name = strings.TrimRight(name, "/")
	if name == "" {
		name = "."
	} else if !cleanName(name) {
		Error(w, 404, "404 page not found")
		return
	}

	f, err := fs.root.Open(name)
	if err != nil {
		Error(w, 404, "404 page not found")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || (trailingSlash && !info.IsDir()) {
		Error(w, 404, "404 page not found")
		return
	}

	if info.IsDir() {
		fs.serveDir(w, r, name, f)
		return
	}
	if !info.Mode().IsRegular() {
		Error(w, 404, "404 page not found")
		return
	}
	serveContent(w, r, info.Name(), info.ModTime(), info.Size(), f)
}

// cleanName reports whether name consists only of ordinary segments.
func cleanName(name string) bool {
	if strings.ContainsAny(name, "\\\x00") {
		return false
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

func (fs *FileServer) serveDir(w *ResponseWriter, r *Request, name string, dir *os.File) {
	// A directory must be requested with a trailing slash, or the relative
	// links inside it would resolve one level too high. Leading slashes are
	// collapsed: "//host/" in Location would be a protocol-relative URL.
	if !strings.HasSuffix(r.Path, "/") {
		w.Header().Set("Location", "/"+strings.TrimLeft(r.Path, "/")+"/")
		w.WriteHeader(301)
		return
	}

	if idx, err := fs.root.Open(path.Join(name, "index.html")); err == nil {
		defer idx.Close()
		if info, err := idx.Stat(); err == nil && info.Mode().IsRegular() {
			serveContent(w, r, "index.html", info.ModTime(), info.Size(), idx)
			return
		}
	}
	if !fs.ListDirs {
		Error(w, 404, "404 page not found")
		return
	}

	entries, err := dir.ReadDir(-1)
	if err != nil {
		Error(w, 500, "500 internal server error")
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var b strings.Builder
	b.WriteString("<!doctype html><meta charset=utf-8><title>Index of " + html.EscapeString(r.Path) + "</title>\n<h1>Index of " +
		html.EscapeString(r.Path) + "</h1>\n<ul>\n")
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() {
			n += "/"
		}
		b.WriteString("<li><a href=\"" + html.EscapeString((&url.URL{Path: n}).EscapedPath()) + "\">" + html.EscapeString(n) + "</a></li>\n")
	}
	b.WriteString("</ul>\n")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == "HEAD" {
		w.Header().Set("Content-Length", strconv.Itoa(b.Len()))
		return
	}
	io.WriteString(w, b.String())
}

// serveContent sends one regular file with validators and range support.
func serveContent(w *ResponseWriter, r *Request, name string, modTime time.Time, size int64, content io.ReaderAt) {
	h := w.Header()
	ctype := mime.TypeByExtension(path.Ext(name))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	h.Set("Content-Type", ctype)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Accept-Ranges", "bytes")

	lastModified := ""
	if !modTime.IsZero() {
		modTime = modTime.UTC().Truncate(time.Second)
		lastModified = modTime.Format(timeFormat)
		h.Set("Last-Modified", lastModified)
	}
	etag := `W/"` + strconv.FormatInt(size, 16) + "-" + strconv.FormatInt(modTime.UnixNano(), 16) + `"`
	h.Set("ETag", etag)

	if notModified(r, etag, modTime) {
		w.WriteHeader(304)
		return
	}

	status, start, length := 200, int64(0), size
	if rng := r.Header.Get("Range"); rng != "" && rangeApplies(r, lastModified) {
		s, l, ok, satisfiable := parseRange(rng, size)
		switch {
		case ok && !satisfiable:
			h.Set("Content-Range", "bytes */"+strconv.FormatInt(size, 10))
			Error(w, 416, "416 range not satisfiable")
			return
		case ok:
			status, start, length = 206, s, l
			h.Set("Content-Range", "bytes "+strconv.FormatInt(s, 10)+"-"+strconv.FormatInt(s+l-1, 10)+"/"+strconv.FormatInt(size, 10))
		}
	}

	h.Set("Content-Length", strconv.FormatInt(length, 10))
	w.WriteHeader(status)
	if r.Method == "HEAD" {
		return
	}
	io.Copy(w, io.NewSectionReader(content, start, length))
}

// notModified evaluates If-None-Match, which takes precedence, and
// If-Modified-Since (RFC 9110 §13.1.3, §13.1.4) for GET and HEAD.
func notModified(r *Request, etag string, modTime time.Time) bool {
	if inm := r.Header.Values("If-None-Match"); len(inm) > 0 {
		for _, v := range inm {
			for _, tag := range strings.Split(v, ",") {
				tag = strings.TrimSpace(tag)
				if tag == "*" || strings.TrimPrefix(tag, "W/") == strings.TrimPrefix(etag, "W/") {
					return true
				}
			}
		}
		return false
	}
	if ims := r.Header.Get("If-Modified-Since"); ims != "" && !modTime.IsZero() {
		if t, err := time.Parse(timeFormat, ims); err == nil && !modTime.After(t) {
			return true
		}
	}
	return false
}

// rangeApplies handles If-Range: the range is honoured only if the validator
// still matches. Our ETag is weak and If-Range requires a strong match, so
// only the Last-Modified date can match.
func rangeApplies(r *Request, lastModified string) bool {
	ifRange := r.Header.Get("If-Range")
	return ifRange == "" || (lastModified != "" && ifRange == lastModified)
}

// parseRange parses a single "bytes=a-b", "bytes=a-" or "bytes=-n" range. ok
// is false when the header must be ignored (other unit, several ranges, bad
// syntax); satisfiable is false when the range lies outside the file.
func parseRange(header string, size int64) (start, length int64, ok, satisfiable bool) {
	spec, found := strings.CutPrefix(header, "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, 0, false, false
	}
	first, last, found := strings.Cut(strings.TrimSpace(spec), "-")
	if !found {
		return 0, 0, false, false
	}

	if first == "" { // Suffix range: the last n bytes.
		n, err := parseDigits(last)
		if err != nil || n == 0 {
			return 0, 0, err == nil, false
		}
		if n > size {
			n = size
		}
		return size - n, n, size > 0, size > 0
	}

	a, err := parseDigits(first)
	if err != nil {
		return 0, 0, false, false
	}
	b := size - 1
	if last != "" {
		if b, err = parseDigits(last); err != nil || b < a {
			return 0, 0, false, false
		}
	}
	if a >= size {
		return 0, 0, true, false
	}
	if b >= size {
		b = size - 1
	}
	return a, b - a + 1, true, true
}

func parseDigits(s string) (int64, error) {
	if s == "" || strings.Trim(s, "0123456789") != "" {
		return 0, os.ErrInvalid
	}
	return strconv.ParseInt(s, 10, 64)
}
