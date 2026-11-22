package http1

import (
	"io"
	"sort"
	"strings"
)

// Router dispatches requests to handlers by method and path.
//
// Patterns are slash-separated segments. A segment is a literal, a wildcard
// {name} matching exactly one non-empty segment, or, as the last segment,
// {name...} matching one or more remaining segments:
//
//	/users/{id}          /files/{path...}
//
// When several patterns match, a literal wins over {name}, which wins over
// {name...}. Matching is on the decoded path, one segment at a time, so %2F
// stays inside its segment and never acts as a separator. Paths with
// invalid percent-encoding, control characters, or "." and ".." segments are
// rejected with 400 before any handler runs.
//
// Besides the registered methods the Router answers HEAD from GET handlers
// and OPTIONS with an Allow header, and replies 405 with Allow when the path
// exists but the method does not. Register all routes before serving; the
// Router is read-only afterwards and safe for concurrent use.
type Router struct {
	// NotFound handles requests that match no pattern. Nil means a plain 404.
	NotFound Handler

	// MethodNotAllowed handles requests whose path matches but whose method
	// does not; Allow is already set. Nil means a plain 405.
	MethodNotAllowed Handler

	root node
}

// Handle registers h for method and pattern. It panics on a malformed
// pattern, a duplicate route, or wildcards that conflict with an existing
// route: all of these are programming errors best found at start-up.
func (rt *Router) Handle(method, pattern string, h Handler) {
	if !isToken(method) {
		panic("http1: invalid method " + method)
	}
	if h == nil {
		panic("http1: nil handler for " + method + " " + pattern)
	}

	n := &rt.root
	for _, seg := range parsePattern(pattern) {
		switch seg.kind {
		case segLiteral:
			if n.static == nil {
				n.static = make(map[string]*node)
			}
			if n.static[seg.text] == nil {
				n.static[seg.text] = &node{}
			}
			n = n.static[seg.text]
		case segParam:
			n.param = wildcardChild(n.param, seg.text, pattern)
			n = n.param
		case segCatchAll:
			n.catchAll = wildcardChild(n.catchAll, seg.text, pattern)
			n = n.catchAll
		}
	}

	if _, dup := n.handlers[method]; dup {
		panic("http1: duplicate route " + method + " " + pattern)
	}
	if n.handlers == nil {
		n.handlers = make(map[string]Handler)
	}
	n.handlers[method] = h
}

// HandleFunc registers f for method and pattern.
func (rt *Router) HandleFunc(method, pattern string, f func(w *ResponseWriter, r *Request)) {
	rt.Handle(method, pattern, HandlerFunc(f))
}

// ServeHTTP implements Handler.
func (rt *Router) ServeHTTP(w *ResponseWriter, r *Request) {
	switch r.Path {
	case "*": // OPTIONS * asks about the server as a whole.
		w.Header().Set("Allow", allowHeader(rt.root.allMethods(map[string]struct{}{})))
		w.WriteHeader(200)
		return
	case "": // CONNECT is not supported.
		Error(w, 501, "Not Implemented")
		return
	}

	segs, ok := splitPath(r.Path)
	if !ok {
		Error(w, 400, "Bad Request")
		return
	}

	var (
		found    Handler
		values   []pathValue
		scratch  []pathValue
		allowed  = make(map[string]struct{})
		sawRoute bool
	)
	rt.root.walk(segs, &scratch, func(n *node, ps []pathValue) bool {
		sawRoute = true
		for m := range n.handlers {
			allowed[m] = struct{}{}
		}
		if h := n.handler(r.Method); h != nil {
			found, values = h, append([]pathValue(nil), ps...)
			return true
		}
		return false
	})

	switch {
	case found != nil:
		r.pathValues = values
		found.ServeHTTP(w, r)
	case !sawRoute:
		if rt.NotFound != nil {
			rt.NotFound.ServeHTTP(w, r)
			return
		}
		Error(w, 404, "404 page not found")
	default:
		w.Header().Set("Allow", allowHeader(allowed))
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		if rt.MethodNotAllowed != nil {
			rt.MethodNotAllowed.ServeHTTP(w, r)
			return
		}
		Error(w, 405, "405 method not allowed")
	}
}

// Error replies with a plain-text error message and the given status.
func Error(w *ResponseWriter, status int, msg string) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	io.WriteString(w, msg+"\n")
}

// PathValue returns the value matched by the wildcard name in the route
// pattern, or "" if there is none. Values are percent-decoded; for
// {name...} the decoded segments are joined with "/".
func (r *Request) PathValue(name string) string {
	for _, p := range r.pathValues {
		if p.name == name {
			return p.value
		}
	}
	return ""
}

type pathValue struct{ name, value string }

// node is one segment position in the route tree.
type node struct {
	static   map[string]*node
	param    *node  // {name}
	catchAll *node  // {name...}
	name     string // Wildcard name, for param and catchAll nodes.
	handlers map[string]Handler
}

// handler returns the handler for method; HEAD falls back to GET, whose body
// the ResponseWriter already discards.
func (n *node) handler(method string) Handler {
	if h := n.handlers[method]; h != nil {
		return h
	}
	if method == "HEAD" {
		return n.handlers["GET"]
	}
	return nil
}

// walk visits every node matching segs in priority order (literal, then
// {name}, then {name...}) until visit returns true. Trying alternatives lets
// /a/{x}/d match /a/b/d even though a literal /a/b/c route exists.
func (n *node) walk(segs []string, params *[]pathValue, visit func(*node, []pathValue) bool) bool {
	if len(segs) == 0 {
		return len(n.handlers) > 0 && visit(n, *params)
	}
	seg, rest := segs[0], segs[1:]

	if c := n.static[seg]; c != nil && c.walk(rest, params, visit) {
		return true
	}
	if c := n.param; c != nil && seg != "" {
		*params = append(*params, pathValue{c.name, seg})
		if c.walk(rest, params, visit) {
			return true
		}
		*params = (*params)[:len(*params)-1]
	}
	if c := n.catchAll; c != nil {
		*params = append(*params, pathValue{c.name, strings.Join(segs, "/")})
		if visit(c, *params) {
			return true
		}
		*params = (*params)[:len(*params)-1]
	}
	return false
}

func (n *node) allMethods(set map[string]struct{}) map[string]struct{} {
	for m := range n.handlers {
		set[m] = struct{}{}
	}
	for _, c := range n.static {
		c.allMethods(set)
	}
	if n.param != nil {
		n.param.allMethods(set)
	}
	if n.catchAll != nil {
		n.catchAll.allMethods(set)
	}
	return set
}

func wildcardChild(existing *node, name, pattern string) *node {
	if existing == nil {
		return &node{name: name}
	}
	if existing.name != name {
		panic("http1: pattern " + pattern + " names a wildcard {" + name +
			"} where an earlier route uses {" + existing.name + "}")
	}
	return existing
}

type segKind int

const (
	segLiteral segKind = iota
	segParam
	segCatchAll
)

type patternSeg struct {
	kind segKind
	text string // Literal text or wildcard name.
}

func parsePattern(pattern string) []patternSeg {
	bad := func(why string) string { return "http1: bad pattern " + pattern + ": " + why }
	if !strings.HasPrefix(pattern, "/") {
		panic(bad("must start with /"))
	}
	parts := strings.Split(pattern[1:], "/")
	segs := make([]patternSeg, 0, len(parts))
	seen := make(map[string]bool)

	for i, p := range parts {
		switch {
		case strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}"):
			name, kind := p[1:len(p)-1], segParam
			if n, ok := strings.CutSuffix(name, "..."); ok {
				name, kind = n, segCatchAll
			}
			if !validParamName(name) {
				panic(bad("wildcard names use letters, digits and _"))
			}
			if seen[name] {
				panic(bad("wildcard {" + name + "} used twice"))
			}
			seen[name] = true
			if kind == segCatchAll && i != len(parts)-1 {
				panic(bad("{" + name + "...} must be the last segment"))
			}
			segs = append(segs, patternSeg{kind, name})
		case strings.ContainsAny(p, "{}"):
			panic(bad("a wildcard must be a whole segment"))
		case p == "." || p == "..":
			panic(bad("dot segments can never match"))
		default:
			segs = append(segs, patternSeg{segLiteral, p})
		}
	}
	return segs
}

func validParamName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c == '_' || '0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z') {
			return false
		}
	}
	return true
}

// splitPath splits a raw path into decoded segments. It fails for paths that
// could be used to escape the intended tree or smuggle control characters.
func splitPath(raw string) ([]string, bool) {
	if raw == "" || raw[0] != '/' {
		return nil, false
	}
	segs := strings.Split(raw[1:], "/")
	for i, s := range segs {
		d, ok := unescapeSegment(s)
		if !ok || d == "." || d == ".." {
			return nil, false
		}
		segs[i] = d
	}
	return segs, true
}

// unescapeSegment decodes %XX sequences. It fails on malformed escapes and on
// decoded control characters, including %00.
func unescapeSegment(s string) (string, bool) {
	if !strings.Contains(s, "%") {
		return s, true
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '%' {
			if i+2 >= len(s) || !isHexDigit(s[i+1]) || !isHexDigit(s[i+2]) {
				return "", false
			}
			c = unhex(s[i+1])<<4 | unhex(s[i+2])
			i += 2
		}
		if c < 0x20 || c == 0x7f {
			return "", false
		}
		b = append(b, c)
	}
	return string(b), true
}

func unhex(c byte) byte {
	switch {
	case '0' <= c && c <= '9':
		return c - '0'
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10
	}
	return c - 'A' + 10
}

// allowHeader renders the Allow value, adding what the Router answers by
// itself: HEAD wherever GET exists, and OPTIONS everywhere.
func allowHeader(methods map[string]struct{}) string {
	if _, ok := methods["GET"]; ok {
		methods["HEAD"] = struct{}{}
	}
	methods["OPTIONS"] = struct{}{}
	list := make([]string, 0, len(methods))
	for m := range methods {
		list = append(list, m)
	}
	sort.Strings(list)
	return strings.Join(list, ", ")
}
