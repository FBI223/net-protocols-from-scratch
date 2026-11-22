package http1

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

// say returns a handler that writes name and the given path values.
func say(name string, params ...string) Handler {
	return HandlerFunc(func(w *ResponseWriter, r *Request) {
		out := name
		for _, p := range params {
			out += " " + p + "=" + r.PathValue(p)
		}
		io.WriteString(w, out)
	})
}

// route sends one request through rt and parses the response.
func route(t *testing.T, rt *Router, method, target string) (*http.Response, string) {
	t.Helper()
	raw := method + " " + target + " HTTP/1.1\r\nHost: h\r\n\r\n"
	req, err := ReadRequest(bufio.NewReader(strings.NewReader(raw)), DefaultLimits())
	if err != nil {
		t.Fatalf("%q: %v", raw, err)
	}
	var buf bytes.Buffer
	rw := newResponseWriter(bufio.NewWriter(&buf), req)
	rt.ServeHTTP(rw, req)
	if err := rw.finish(); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(&buf), &http.Request{Method: method})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func newTestRouter() *Router {
	rt := &Router{}
	rt.Handle("GET", "/", say("root"))
	rt.Handle("GET", "/users", say("list"))
	rt.Handle("GET", "/users/me", say("me"))
	rt.Handle("POST", "/users/{id}", say("create", "id"))
	rt.Handle("GET", "/users/{id}", say("get", "id"))
	rt.Handle("GET", "/users/{id}/posts/{post}", say("post", "id", "post"))
	rt.Handle("GET", "/files/{path...}", say("file", "path"))
	rt.Handle("GET", "/a/b/c", say("abc"))
	rt.Handle("GET", "/a/{x}/d", say("axd", "x"))
	rt.Handle("GET", "/a b", say("space"))
	rt.Handle("GET", "/x/{v}", say("x", "v"))
	rt.Handle("GET", "/dir/", say("dir"))
	rt.Handle("GET", "/head", say("get-head"))
	rt.Handle("HEAD", "/head", HandlerFunc(func(w *ResponseWriter, r *Request) {
		w.Header().Set("X-Explicit", "yes")
	}))
	return rt
}

func TestRouterMatching(t *testing.T) {
	rt := newTestRouter()
	tests := []struct{ method, target, want string }{
		{"GET", "/", "root"},
		{"GET", "/users", "list"},
		{"GET", "/users/42", "get id=42"},
		{"POST", "/users/42", "create id=42"},
		{"GET", "/users/42?x=1&y=2", "get id=42"}, // Query is not part of the path.
		{"GET", "/users/42/posts/7", "post id=42 post=7"},
		{"GET", "/users/me", "me"},            // Literal beats wildcard...
		{"POST", "/users/me", "create id=me"}, // ...but only where it has the method.
		{"GET", "/files/a/b/c.txt", "file path=a/b/c.txt"},
		{"GET", "/files/x", "file path=x"},
		{"GET", "/files/", "file path="},
		{"GET", "/a/b/c", "abc"},
		{"GET", "/a/b/d", "axd x=b"}, // Backtracks from /a/b/c to /a/{x}/d.
		{"GET", "/users/caf%C3%A9", "get id=café"},
		{"GET", "/a%20b", "space"},
		{"GET", "/x/a%2Fb", "x v=a/b"}, // %2F stays inside one segment.
		{"GET", "/dir/", "dir"},
	}
	for _, tc := range tests {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			resp, body := route(t, rt, tc.method, tc.target)
			if resp.StatusCode != 200 || body != tc.want {
				t.Errorf("got %d %q, want %q", resp.StatusCode, body, tc.want)
			}
		})
	}
}

func TestRouterNotFound(t *testing.T) {
	rt := newTestRouter()
	for _, target := range []string{
		"/nope", "/Users", "/users/42/", "/users/42/posts", "/files", "/x/a/b",
		"/users//posts", "//", "/dir", "/a/b",
	} {
		t.Run(target, func(t *testing.T) {
			resp, body := route(t, rt, "GET", target)
			if resp.StatusCode != 404 || body != "404 page not found\n" {
				t.Errorf("got %d %q", resp.StatusCode, body)
			}
		})
	}
}

func TestRouterBadPaths(t *testing.T) {
	rt := newTestRouter()
	for _, target := range []string{
		"/bad%zz", "/bad%", "/bad%4", "/a%00b", "/a%0Ab", "/a%7f",
		"/a/../b", "/a/./b", "/a/%2e%2e/b", "/a/%2E/b", "/..", "/users/..",
	} {
		t.Run(target, func(t *testing.T) {
			if resp, _ := route(t, rt, "GET", target); resp.StatusCode != 400 {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestRouterMethodNotAllowed(t *testing.T) {
	rt := newTestRouter()

	// Allow is the union over every pattern that matches the path.
	resp, body := route(t, rt, "DELETE", "/users/me")
	if resp.StatusCode != 405 || resp.Header.Get("Allow") != "GET, HEAD, OPTIONS, POST" || body != "405 method not allowed\n" {
		t.Errorf("got %d Allow=%q %q", resp.StatusCode, resp.Header.Get("Allow"), body)
	}
	resp, _ = route(t, rt, "PUT", "/")
	if resp.StatusCode != 405 || resp.Header.Get("Allow") != "GET, HEAD, OPTIONS" {
		t.Errorf("got %d Allow=%q", resp.StatusCode, resp.Header.Get("Allow"))
	}
}

func TestRouterHeadAndOptions(t *testing.T) {
	rt := newTestRouter()

	resp, body := route(t, rt, "HEAD", "/users/42")
	if resp.StatusCode != 200 || body != "" || resp.ContentLength != int64(len("get id=42")) {
		t.Errorf("HEAD from GET: %d len=%d %q", resp.StatusCode, resp.ContentLength, body)
	}
	if resp, _ := route(t, rt, "HEAD", "/head"); resp.Header.Get("X-Explicit") != "yes" {
		t.Error("explicit HEAD handler not preferred over GET")
	}

	resp, _ = route(t, rt, "OPTIONS", "/users/42")
	if resp.StatusCode != 204 || resp.Header.Get("Allow") != "GET, HEAD, OPTIONS, POST" {
		t.Errorf("OPTIONS: %d Allow=%q", resp.StatusCode, resp.Header.Get("Allow"))
	}
	resp, _ = route(t, rt, "OPTIONS", "*")
	if resp.StatusCode != 200 || resp.Header.Get("Allow") != "GET, HEAD, OPTIONS, POST" {
		t.Errorf("OPTIONS *: %d Allow=%q", resp.StatusCode, resp.Header.Get("Allow"))
	}
	if resp, _ := route(t, rt, "OPTIONS", "/nope"); resp.StatusCode != 404 {
		t.Errorf("OPTIONS on unknown path: %d", resp.StatusCode)
	}
	if resp, _ := route(t, rt, "CONNECT", "h:443"); resp.StatusCode != 501 {
		t.Errorf("CONNECT: %d", resp.StatusCode)
	}
}

func TestRouterCustomFallbacks(t *testing.T) {
	rt := &Router{
		NotFound: say("custom-404"),
		MethodNotAllowed: HandlerFunc(func(w *ResponseWriter, r *Request) {
			w.WriteHeader(418)
			io.WriteString(w, "allow="+w.Header().Get("Allow"))
		}),
	}
	rt.Handle("GET", "/x", say("x"))
	if _, body := route(t, rt, "GET", "/y"); body != "custom-404" {
		t.Errorf("NotFound body = %q", body)
	}
	if resp, body := route(t, rt, "POST", "/x"); resp.StatusCode != 418 || body != "allow=GET, HEAD, OPTIONS" {
		t.Errorf("MethodNotAllowed: %d %q", resp.StatusCode, body)
	}
}

func TestRouterEmpty(t *testing.T) {
	rt := &Router{}
	if resp, _ := route(t, rt, "GET", "/"); resp.StatusCode != 404 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if resp, _ := route(t, rt, "OPTIONS", "*"); resp.StatusCode != 200 || resp.Header.Get("Allow") != "OPTIONS" {
		t.Errorf("OPTIONS *: %d Allow=%q", resp.StatusCode, resp.Header.Get("Allow"))
	}
}

func TestRouterPathValueUnknown(t *testing.T) {
	var got string
	rt := &Router{}
	rt.HandleFunc("GET", "/u/{id}", func(w *ResponseWriter, r *Request) { got = r.PathValue("other") })
	route(t, rt, "GET", "/u/1")
	if got != "" {
		t.Errorf("PathValue(other) = %q", got)
	}
}

func TestRouterRegistrationPanics(t *testing.T) {
	h := say("h")
	tests := map[string]func(*Router){
		"duplicate":            func(rt *Router) { rt.Handle("GET", "/a", h); rt.Handle("GET", "/a", h) },
		"duplicate wildcard":   func(rt *Router) { rt.Handle("GET", "/a/{x}", h); rt.Handle("GET", "/a/{x}", h) },
		"conflicting names":    func(rt *Router) { rt.Handle("GET", "/a/{x}", h); rt.Handle("POST", "/a/{y}", h) },
		"conflicting catchall": func(rt *Router) { rt.Handle("GET", "/a/{x...}", h); rt.Handle("POST", "/a/{y...}", h) },
		"no leading slash":     func(rt *Router) { rt.Handle("GET", "a", h) },
		"empty pattern":        func(rt *Router) { rt.Handle("GET", "", h) },
		"empty name":           func(rt *Router) { rt.Handle("GET", "/a/{}", h) },
		"empty catchall name":  func(rt *Router) { rt.Handle("GET", "/a/{...}", h) },
		"bad name":             func(rt *Router) { rt.Handle("GET", "/a/{x-y}", h) },
		"partial wildcard":     func(rt *Router) { rt.Handle("GET", "/a/pre{x}", h) },
		"stray brace":          func(rt *Router) { rt.Handle("GET", "/a/{", h) },
		"catchall not last":    func(rt *Router) { rt.Handle("GET", "/a/{x...}/b", h) },
		"repeated name":        func(rt *Router) { rt.Handle("GET", "/{x}/{x}", h) },
		"dot segment":          func(rt *Router) { rt.Handle("GET", "/a/../b", h) },
		"bad method":           func(rt *Router) { rt.Handle("GE T", "/a", h) },
		"empty method":         func(rt *Router) { rt.Handle("", "/a", h) },
		"nil handler":          func(rt *Router) { rt.Handle("GET", "/a", nil) },
	}
	for name, fn := range tests {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("expected panic")
				}
			}()
			fn(&Router{})
		})
	}
}

// Same wildcard name in different methods or branches is fine.
func TestRouterNoFalseConflicts(t *testing.T) {
	rt := &Router{}
	rt.Handle("GET", "/a/{x}", say("1"))
	rt.Handle("POST", "/a/{x}", say("2"))
	rt.Handle("GET", "/a/{x}/b", say("3"))
	rt.Handle("GET", "/b/{y}", say("4"))
	rt.Handle("GET", "/a/{x...}", say("5")) // Different child slot than {x}.
}

func TestRouterOverRealServer(t *testing.T) {
	rt := newTestRouter()
	addr, _ := startServer(t, rt.ServeHTTP, Limits{})
	resp, err := http.Get("http://" + addr + "/users/42/posts/7")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "post id=42 post=7" {
		t.Errorf("got %d %q", resp.StatusCode, body)
	}
}
