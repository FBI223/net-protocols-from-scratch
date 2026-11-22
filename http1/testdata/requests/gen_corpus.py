#!/usr/bin/env python3
"""Generates the raw request corpus and its expectation manifest.

Run from this directory:  python3 gen_corpus.py

Every file is written byte for byte (CRLF, NUL, bytes above 0x7f), which is
hard to do reliably in an editor. expect.json records what a correct parser
must do with each file: the requests it yields (head and decoded body) and
the error that ends the stream, or "" for a clean end of input.
"""
import base64, hashlib, json, os, shutil

HERE = os.path.dirname(os.path.abspath(__file__))
files = {}  # relative path -> (bytes, expectation)

def raw(*lines, body=b""):
    """Head lines joined with CRLF, a blank line, then the body."""
    return ("\r\n".join(lines) + "\r\n\r\n").encode("latin-1") + body

def chunked(parts, ext="", trailers=()):
    out = b""
    for p in parts:
        out += b"%x%s\r\n" % (len(p), ext.encode()) + p + b"\r\n"
    out += b"0\r\n" + b"".join(t.encode("latin-1") + b"\r\n" for t in trailers) + b"\r\n"
    return out

def pattern(n, seed=3):
    return bytes((i * 7 + seed) % 256 for i in range(n))

def E(method="GET", path="/", host="h", minor=1, body=b"", query="", headers=None):
    e = {"method": method, "path": path, "query": query, "host": host, "minor": minor}
    if len(body) > 4096:
        e["body_len"], e["body_sha256"] = len(body), hashlib.sha256(body).hexdigest()
    elif all(0x20 <= b < 0x7f or b in b"\r\n\t" for b in body):
        e["body"] = body.decode("ascii")
    else:
        e["body_b64"] = base64.b64encode(body).decode()
    if headers:
        e["headers"] = headers
    return e

def add(path, data, requests, err):
    assert path not in files, path
    files[path] = (data, {"requests": requests, "error": err})

def valid(name, data, *requests, err=""):
    add("valid/%s.raw" % name, data, list(requests), err)

def invalid(name, data, err, *requests):
    add("invalid/%s.raw" % name, data, list(requests), err)

H = "Host: h"
GET = raw("GET / HTTP/1.1", H)

# --------------------------------------------------------------- methods
for m in ["GET", "HEAD", "DELETE", "TRACE"]:
    valid("method-%s" % m.lower(), raw("%s / HTTP/1.1" % m, H), E(m))
for m in ["POST", "PUT", "PATCH"]:
    valid("method-%s" % m.lower(), raw("%s /r HTTP/1.1" % m, H, "Content-Length: 5", body=b"hello"),
          E(m, "/r", body=b"hello", headers={"Content-Length": ["5"]}))
valid("method-options-star", raw("OPTIONS * HTTP/1.1", H), E("OPTIONS", "*"))
valid("method-options-path", raw("OPTIONS /x HTTP/1.1", H), E("OPTIONS", "/x"))
valid("method-custom-token", raw("M-SEARCH /x HTTP/1.1", H), E("M-SEARCH", "/x"))
valid("method-lowercase-is-a-token", raw("get / HTTP/1.1", H), E("get"))
valid("method-long-token", raw("%s / HTTP/1.1" % ("A" * 64), H), E("A" * 64))
valid("method-connect", raw("CONNECT example.com:443 HTTP/1.1", "Host: example.com:443"),
      E("CONNECT", "", host="example.com:443"))

# --------------------------------------------------------------- targets
valid("target-deep-path", raw("GET /a/b/c/d/e/f/g HTTP/1.1", H), E(path="/a/b/c/d/e/f/g"))
valid("target-query", raw("GET /search?q=go&lang=pl HTTP/1.1", H), E(path="/search", query="q=go&lang=pl"))
valid("target-empty-query", raw("GET /p? HTTP/1.1", H), E(path="/p"))
valid("target-question-mark-in-query", raw("GET /p?a=b?c=d HTTP/1.1", H), E(path="/p", query="a=b?c=d"))
valid("target-percent-encoded", raw("GET /caf%C3%A9/a%20b HTTP/1.1", H), E(path="/caf%C3%A9/a%20b"))
valid("target-sub-delims", raw("GET /a;b=c/(x)/!$&'*+,=:@ HTTP/1.1", H), E(path="/a;b=c/(x)/!$&'*+,=:@"))
valid("target-tilde-and-dots", raw("GET /~user/file.tar.gz HTTP/1.1", H), E(path="/~user/file.tar.gz"))
valid("target-double-slash", raw("GET //a//b HTTP/1.1", H), E(path="//a//b"))
# The parser only checks syntax; dot segments and bad escapes are the Router's job.
valid("target-dot-segments-reach-router", raw("GET /a/../b/./c HTTP/1.1", H), E(path="/a/../b/./c"))
valid("target-bad-escape-reaches-router", raw("GET /%zz HTTP/1.1", H), E(path="/%zz"))
valid("target-long-but-within-limit", raw("GET /%s HTTP/1.1" % ("a" * 7000), H), E(path="/" + "a" * 7000))
valid("target-absolute-http", raw("GET http://example.com/a?b=c HTTP/1.1", "Host: ignored.example"),
      E(path="/a", query="b=c", host="example.com"))
valid("target-absolute-https-port", raw("GET https://example.com:8443/x HTTP/1.1", H), E(path="/x", host="example.com:8443"))
valid("target-absolute-no-path", raw("GET http://example.com HTTP/1.1", H), E(path="/", host="example.com"))
valid("target-absolute-query-only", raw("GET http://example.com?x=1 HTTP/1.1", H), E(path="/", query="x=1", host="example.com"))
valid("target-absolute-uppercase-scheme", raw("GET HTTP://EXAMPLE.com/p HTTP/1.1", H), E(path="/p", host="EXAMPLE.com"))
valid("target-absolute-ipv6", raw("GET http://[::1]:8080/p HTTP/1.1", H), E(path="/p", host="[::1]:8080"))

# -------------------------------------------------------------- versions
valid("version-http10-no-host", raw("GET / HTTP/1.0"), E(host="", minor=0))
valid("version-http10-with-host", raw("GET / HTTP/1.0", H), E(minor=0))
valid("version-http10-keep-alive-header", raw("GET / HTTP/1.0", "Connection: keep-alive"),
      E(host="", minor=0, headers={"Connection": ["keep-alive"]}))
valid("version-http11-connection-close", raw("GET / HTTP/1.1", H, "Connection: close"),
      E(headers={"Connection": ["close"]}))
valid("version-higher-minor-needs-host", raw("GET / HTTP/1.9", H), E(minor=9))

# --------------------------------------------------------------- headers
valid("header-case-insensitive-names", raw("GET / HTTP/1.1", "hOsT: h", "aCCePt: x", "X-FOO: y"),
      E(headers={"Accept": ["x"], "X-Foo": ["y"]}))
valid("header-ows-tabs-and-spaces", raw("GET / HTTP/1.1", H, "X-A:\t v \t", "X-B:   w"),
      E(headers={"X-A": ["v"], "X-B": ["w"]}))
valid("header-no-ows", raw("GET / HTTP/1.1", "Host:h", "X-A:v"), E(headers={"X-A": ["v"]}))
valid("header-empty-value", raw("GET / HTTP/1.1", H, "X-Empty:"), E(headers={"X-Empty": [""]}))
valid("header-empty-value-spaces", raw("GET / HTTP/1.1", H, "X-Empty:   \t "), E(headers={"X-Empty": [""]}))
valid("header-repeated-keeps-order", raw("GET / HTTP/1.1", H, "Accept: a", "Accept: b", "Accept: c"),
      E(headers={"Accept": ["a", "b", "c"]}))
valid("header-comma-list-single-line", raw("GET / HTTP/1.1", H, "Accept: text/html, application/json;q=0.9"),
      E(headers={"Accept": ["text/html, application/json;q=0.9"]}))
valid("header-colons-in-value", raw("GET / HTTP/1.1", H, "X-U: a:b:c"), E(headers={"X-U": ["a:b:c"]}))
valid("header-quoted-value", raw("GET / HTTP/1.1", H, 'X-Q: "quoted, value"; a=b'), E(headers={"X-Q": ['"quoted, value"; a=b']}))
valid("header-obs-text-bytes", raw("GET / HTTP/1.1", H, "X-Bytes: caf\xe9 \xff"), E())
valid("header-unusual-token-name", raw("GET / HTTP/1.1", H, "X-Weird_name.v1~: v"), E(headers={"X-Weird_name.v1~": ["v"]}))
valid("header-host-with-port", raw("GET / HTTP/1.1", "Host: example.com:8080"), E(host="example.com:8080"))
valid("header-host-ipv6", raw("GET / HTTP/1.1", "Host: [::1]:8080"), E(host="[::1]:8080"))
valid("header-host-trailing-spaces", raw("GET / HTTP/1.1", "Host: h  \t"), E())
valid("header-99-extra-headers", raw("GET / HTTP/1.1", H, *["X-N%d: v" % i for i in range(99)]), E())
valid("header-long-value-10k", raw("GET / HTTP/1.1", H, "X-Long: " + "a" * 10000), E(headers={"X-Long": ["a" * 10000]}))
valid("header-two-cookie-lines", raw("GET / HTTP/1.1", H, "Cookie: a=1", "Cookie: b=2"), E(headers={"Cookie": ["a=1", "b=2"]}))
valid("header-websocket-upgrade", raw("GET /chat HTTP/1.1", "Host: server.example.com", "Upgrade: websocket",
      "Connection: Upgrade", "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==", "Sec-WebSocket-Version: 13"),
      E(path="/chat", host="server.example.com", headers={"Upgrade": ["websocket"], "Connection": ["Upgrade"]}))
valid("header-expect-continue", raw("POST /up HTTP/1.1", H, "Content-Length: 5", "Expect: 100-continue", body=b"hello"),
      E("POST", "/up", body=b"hello", headers={"Expect": ["100-continue"]}))

# ------------------------------------------- realistic clients and payloads
valid("client-curl", raw("GET /index.html HTTP/1.1", "Host: localhost:8080", "User-Agent: curl/8.5.0", "Accept: */*"),
      E(path="/index.html", host="localhost:8080", headers={"User-Agent": ["curl/8.5.0"], "Accept": ["*/*"]}))
valid("client-chrome", raw("GET /index.html HTTP/1.1", "Host: localhost:8080", "Connection: keep-alive",
      "Cache-Control: max-age=0", 'sec-ch-ua: "Chromium";v="130", "Not?A_Brand";v="99"', "sec-ch-ua-mobile: ?0",
      "Upgrade-Insecure-Requests: 1",
      "User-Agent: Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
      "Accept: text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
      "Sec-Fetch-Site: none", "Sec-Fetch-Mode: navigate", "Accept-Encoding: gzip, deflate, br, zstd",
      "Accept-Language: pl-PL,pl;q=0.9,en-US;q=0.8", "Cookie: session=abc123; theme=dark"),
      E(path="/index.html", host="localhost:8080", headers={"Sec-Ch-Ua-Mobile": ["?0"], "Cookie": ["session=abc123; theme=dark"],
        "Accept-Language": ["pl-PL,pl;q=0.9,en-US;q=0.8"]}))
js = b'{"name":"Zo\xc3\xab","tags":["a","b"],"n":1}'
valid("client-json-api-post", raw("POST /api/items HTTP/1.1", H, "Content-Type: application/json", "Content-Length: %d" % len(js),
      "Accept: application/json", "Authorization: Bearer abc.def.ghi", body=js),
      E("POST", "/api/items", body=js, headers={"Content-Type": ["application/json"], "Authorization": ["Bearer abc.def.ghi"]}))
form = b"a=1&b=hello+world&c=%C5%BC%C3%B3%C5%82w"
valid("client-urlencoded-form", raw("POST /form HTTP/1.1", H, "Content-Type: application/x-www-form-urlencoded",
      "Content-Length: %d" % len(form), body=form), E("POST", "/form", body=form))
mp = (b"--XyZ\r\nContent-Disposition: form-data; name=\"title\"\r\n\r\nhello\r\n"
      b"--XyZ\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.bin\"\r\nContent-Type: application/octet-stream\r\n\r\n"
      + bytes(range(256)) + b"\r\n--XyZ--\r\n")
valid("client-multipart-upload", raw("POST /upload HTTP/1.1", H, "Content-Type: multipart/form-data; boundary=XyZ",
      "Content-Length: %d" % len(mp), body=mp), E("POST", "/upload", body=mp))

# ------------------------------------------------------ bodies (Content-Length)
valid("body-cl-zero", raw("POST / HTTP/1.1", H, "Content-Length: 0"), E("POST"))
valid("body-cl-leading-zeros", raw("POST / HTTP/1.1", H, "Content-Length: 0000007", body=b"1234567"), E("POST", body=b"1234567"))
valid("body-cl-spaces-around", raw("POST / HTTP/1.1", H, "Content-Length:  5 \t", body=b"hello"), E("POST", body=b"hello"))
valid("body-cl-repeated-identical", raw("POST / HTTP/1.1", H, "Content-Length: 5", "Content-Length: 5", body=b"hello"), E("POST", body=b"hello"))
valid("body-cl-list-identical", raw("POST / HTTP/1.1", H, "Content-Length: 5, 5", body=b"hello"), E("POST", body=b"hello"))
valid("body-get-with-body", raw("GET / HTTP/1.1", H, "Content-Length: 4", body=b"data"), E(body=b"data"))
valid("body-binary-all-bytes", raw("POST / HTTP/1.1", H, "Content-Length: 256", body=bytes(range(256))), E("POST", body=bytes(range(256))))
valid("body-contains-crlf-crlf", raw("POST / HTTP/1.1", H, "Content-Length: 6", body=b"a\r\n\r\nb"), E("POST", body=b"a\r\n\r\nb"))
evil = b"GET /evil HTTP/1.1\r\nHost: h\r\n\r\n"
valid("body-looks-like-a-request", raw("POST / HTTP/1.1", H, "Content-Length: %d" % len(evil), body=evil), E("POST", body=evil))
b100k = pattern(100 * 1024)
valid("body-100k", raw("POST /big HTTP/1.1", H, "Content-Length: %d" % len(b100k), body=b100k), E("POST", "/big", body=b100k))
b1m = pattern(1 << 20, 5)
valid("limit-body-exactly-1mib", raw("POST /big HTTP/1.1", H, "Content-Length: %d" % len(b1m), body=b1m), E("POST", "/big", body=b1m))

# ----------------------------------------------------------- bodies (chunked)
TE = "Transfer-Encoding: chunked"
def post_chunked(body_bytes, te=TE, extra=()):
    return raw("POST /c HTTP/1.1", H, te, *extra, body=body_bytes)
valid("chunked-simple", post_chunked(chunked([b"hello"])), E("POST", "/c", body=b"hello"))
valid("chunked-multiple-chunks", post_chunked(chunked([b"hello", b" ", b"world"])), E("POST", "/c", body=b"hello world"))
valid("chunked-empty-body", post_chunked(b"0\r\n\r\n"), E("POST", "/c"))
valid("chunked-hex-uppercase", post_chunked(b"A\r\n0123456789\r\n0\r\n\r\n"), E("POST", "/c", body=b"0123456789"))
valid("chunked-hex-lowercase", post_chunked(b"a\r\n0123456789\r\n0\r\n\r\n"), E("POST", "/c", body=b"0123456789"))
valid("chunked-leading-zeros", post_chunked(b"0005\r\nhello\r\n0\r\n\r\n"), E("POST", "/c", body=b"hello"))
valid("chunked-extension", post_chunked(chunked([b"hello"], ext=";name=value;x")), E("POST", "/c", body=b"hello"))
valid("chunked-extension-spaces", post_chunked(b"5 ;a=b\r\nhello\r\n0 ;done\r\n\r\n"), E("POST", "/c", body=b"hello"))
valid("chunked-trailers", post_chunked(chunked([b"hello"], trailers=["X-Sum: 1", "X-Other: 2"])), E("POST", "/c", body=b"hello"))
valid("chunked-te-uppercase", post_chunked(chunked([b"hi"]), te="TRANSFER-ENCODING: CHUNKED"), E("POST", "/c", body=b"hi"))
valid("chunked-te-tab-ows", post_chunked(chunked([b"hi"]), te="Transfer-Encoding:\tchunked"), E("POST", "/c", body=b"hi"))
valid("chunked-one-byte-chunks", post_chunked(chunked([bytes([65 + i % 26]) for i in range(50)])),
      E("POST", "/c", body=bytes(65 + i % 26 for i in range(50))))
valid("chunked-data-looks-like-terminator", post_chunked(chunked([b"0\r\n\r\n", b"after"])), E("POST", "/c", body=b"0\r\n\r\nafter"))
valid("chunked-binary", post_chunked(chunked([bytes(range(256))])), E("POST", "/c", body=bytes(range(256))))
big64 = pattern(64 * 1024)
valid("chunked-64k-single-chunk", post_chunked(chunked([big64])), E("POST", "/c", body=big64))
valid("limit-chunked-exactly-1mib", post_chunked(chunked([b1m[:512 * 1024], b1m[512 * 1024:]])), E("POST", "/c", body=b1m))
# A chunk-size line of exactly MaxChunkLineBytes (1024 including CRLF) is allowed.
valid("limit-chunk-line-at-limit", post_chunked(b"5;" + b"a" * (1024 - 2 - 2) + b"\r\nhello\r\n0\r\n\r\n"),
      E("POST", "/c", body=b"hello"))

# ------------------------------------------------------------- pipelining
valid("pipeline-two-gets", GET + raw("GET /b HTTP/1.1", H), E(), E(path="/b"))
valid("pipeline-three-gets", GET + raw("GET /b HTTP/1.1", H) + raw("GET /c HTTP/1.1", H), E(), E(path="/b"), E(path="/c"))
valid("pipeline-post-then-get", raw("POST /a HTTP/1.1", H, "Content-Length: 3", body=b"abc") + raw("GET /b HTTP/1.1", H),
      E("POST", "/a", body=b"abc"), E(path="/b"))
valid("pipeline-chunked-then-get", post_chunked(chunked([b"xyz"], trailers=["T: 1"])) + raw("GET /b HTTP/1.1", H),
      E("POST", "/c", body=b"xyz"), E(path="/b"))
valid("pipeline-head-then-post", raw("HEAD /a HTTP/1.1", H) + raw("POST /b HTTP/1.1", H, "Content-Length: 1", body=b"z"),
      E("HEAD", "/a"), E("POST", "/b", body=b"z"))
valid("pipeline-one-empty-line-between", GET + b"\r\n" + raw("GET /b HTTP/1.1", H), E(), E(path="/b"))
valid("pipeline-ten-gets", b"".join(raw("GET /%d HTTP/1.1" % i, H) for i in range(10)), *[E(path="/%d" % i) for i in range(10)])
valid("leading-empty-line", b"\r\n" + GET, E())
valid("empty-input", b"", err="")

# ================================================================ INVALID
# ------------------------------------------------------------ request line
invalid("reqline-missing-version", raw("GET /", H), "ErrMalformedRequestLine")
invalid("reqline-missing-target", raw("GET HTTP/1.1", H), "ErrMalformedRequestLine")
invalid("reqline-only-method", raw("GET", H), "ErrMalformedRequestLine")
invalid("reqline-double-space", raw("GET  / HTTP/1.1", H), "ErrMalformedRequestLine")
invalid("reqline-trailing-space", raw("GET / HTTP/1.1 ", H), "ErrMalformedRequestLine")
# Tokenised as method "", target "GET", version "/ HTTP/1.1" (which has a space).
invalid("reqline-leading-space", raw(" GET / HTTP/1.1", H), "ErrMalformedRequestLine")
invalid("reqline-tab-separators", raw("GET\t/\tHTTP/1.1", H), "ErrMalformedRequestLine")
invalid("reqline-extra-token", raw("GET / HTTP/1.1 extra", H), "ErrMalformedRequestLine")
invalid("reqline-two-empty-lines", b"\r\n\r\n" + GET, "ErrMalformedRequestLine")
invalid("reqline-method-invalid-char", raw("G@T / HTTP/1.1", H), "ErrInvalidMethod")
invalid("reqline-method-with-slash", raw("GE/T / HTTP/1.1", H), "ErrInvalidMethod")
invalid("reqline-version-lowercase", raw("GET / http/1.1", H), "ErrMalformedVersion")
invalid("reqline-version-no-minor", raw("GET / HTTP/1", H), "ErrMalformedVersion")
invalid("reqline-version-two-digit-minor", raw("GET / HTTP/1.11", H), "ErrMalformedVersion")
invalid("reqline-version-leading-zero", raw("GET / HTTP/01.1", H), "ErrMalformedVersion")
invalid("reqline-version-letters", raw("GET / HTTP/a.b", H), "ErrMalformedVersion")
invalid("version-2.0", raw("GET / HTTP/2.0", H), "ErrUnsupportedVersion")
invalid("version-3.0", raw("GET / HTTP/3.0", H), "ErrUnsupportedVersion")
invalid("version-0.9", raw("GET / HTTP/0.9", H), "ErrUnsupportedVersion")
invalid("version-9.9", raw("GET / HTTP/9.9", H), "ErrUnsupportedVersion")
invalid("target-fragment", raw("GET /a#b HTTP/1.1", H), "ErrInvalidTarget")
invalid("target-control-char", raw("GET /a\x01b HTTP/1.1", H), "ErrInvalidTarget")
invalid("target-nul", raw("GET /a\x00b HTTP/1.1", H), "ErrInvalidTarget")
invalid("target-del", raw("GET /a\x7fb HTTP/1.1", H), "ErrInvalidTarget")
invalid("target-non-ascii-byte", raw("GET /caf\xc3\xa9 HTTP/1.1", H), "ErrInvalidTarget")
invalid("target-relative", raw("GET a/b HTTP/1.1", H), "ErrInvalidTarget")
invalid("target-asterisk-with-get", raw("GET * HTTP/1.1", H), "ErrInvalidTarget")
invalid("target-absolute-ftp", raw("GET ftp://h/x HTTP/1.1", H), "ErrInvalidTarget")
invalid("target-absolute-empty-host", raw("GET http:///path HTTP/1.1", H), "ErrInvalidTarget")
invalid("target-absolute-userinfo", raw("GET http://user@h/p HTTP/1.1", H), "ErrInvalidTarget")
invalid("target-connect-with-path", raw("CONNECT h:443/x HTTP/1.1", H), "ErrInvalidTarget")
invalid("target-connect-origin-form", raw("CONNECT /x HTTP/1.1", H), "ErrInvalidTarget")
# Request line limit is 8192 bytes including its CRLF.
valid("limit-request-line-at-limit", raw("GET /%s HTTP/1.1" % ("a" * 8176), H), E(path="/" + "a" * 8176))
invalid("limit-request-line-over-limit", raw("GET /%s HTTP/1.1" % ("a" * 8177), H), "ErrRequestLineTooLong")
invalid("limit-request-line-way-over", raw("GET /%s HTTP/1.1" % ("a" * 60000), H), "ErrRequestLineTooLong")

# -------------------------------------------------------------- line endings
invalid("eol-bare-cr-in-request-line", b"GET / HTTP/1.1\rHost: h\r\n\r\n", "ErrBareCR")
invalid("eol-double-cr", b"GET / HTTP/1.1\r\r\nHost: h\r\n\r\n", "ErrBareCR")
invalid("eol-bare-cr-in-header-value", b"GET / HTTP/1.1\r\nHost: h\r\nX: a\rb\r\n\r\n", "ErrBareCR")
invalid("eol-bare-cr-after-host", b"GET / HTTP/1.1\r\nHost: h\r\r\n\r\n", "ErrBareCR")
invalid("eol-bare-lf-request-line", b"GET / HTTP/1.1\nHost: h\r\n\r\n", "ErrBareLF")
invalid("eol-bare-lf-header", b"GET / HTTP/1.1\r\nHost: h\nX: 1\r\n\r\n", "ErrBareLF")
invalid("eol-all-lf", b"GET / HTTP/1.1\nHost: h\n\n", "ErrBareLF")
invalid("eol-lf-then-cr", b"GET / HTTP/1.1\n\rHost: h\r\n\r\n", "ErrBareLF")
invalid("eol-bare-lf-final-blank-line", b"GET / HTTP/1.1\r\nHost: h\r\n\n", "ErrBareLF")
invalid("eol-cr-only-no-lf", b"GET / HTTP/1.1\rHost: h\r\r", "io.ErrUnexpectedEOF")

# ----------------------------------------------------------------- headers
invalid("hdr-no-host", raw("GET / HTTP/1.1"), "ErrMissingHost")
invalid("hdr-two-host", raw("GET / HTTP/1.1", "Host: a", "Host: b"), "ErrMultipleHost")
invalid("hdr-two-host-identical", raw("GET / HTTP/1.1", "Host: a", "Host: a"), "ErrMultipleHost")
invalid("hdr-host-empty", raw("GET / HTTP/1.1", "Host:"), "ErrInvalidHost")
invalid("hdr-host-inner-space", raw("GET / HTTP/1.1", "Host: a b"), "ErrInvalidHost")
invalid("hdr-host-slash", raw("GET / HTTP/1.1", "Host: a/b"), "ErrInvalidHost")
invalid("hdr-host-userinfo", raw("GET / HTTP/1.1", "Host: user@h"), "ErrInvalidHost")
invalid("hdr-host-backslash", raw("GET / HTTP/1.1", "Host: a\\b"), "ErrInvalidHost")
invalid("hdr-host-hash", raw("GET / HTTP/1.1", "Host: a#b"), "ErrInvalidHost")
invalid("hdr-host-question", raw("GET / HTTP/1.1", "Host: a?b"), "ErrInvalidHost")
invalid("hdr-space-before-colon", raw("GET / HTTP/1.1", "Host : h"), "ErrSpaceBeforeColon")
invalid("hdr-tab-before-colon", raw("GET / HTTP/1.1", "Host\t: h"), "ErrSpaceBeforeColon")
invalid("hdr-space-before-colon-other", raw("GET / HTTP/1.1", H, "X-A : 1"), "ErrSpaceBeforeColon")
invalid("hdr-obs-fold-space", raw("GET / HTTP/1.1", H, "X: a", " b"), "ErrObsFold")
invalid("hdr-obs-fold-tab", raw("GET / HTTP/1.1", H, "X: a", "\tb"), "ErrObsFold")
invalid("hdr-obs-fold-first-header-line", raw("GET / HTTP/1.1", " Host: h"), "ErrObsFold")
invalid("hdr-no-colon", raw("GET / HTTP/1.1", H, "bogus"), "ErrInvalidHeaderName")
invalid("hdr-request-smuggled-as-header", raw("GET / HTTP/1.1", H, "GET /evil HTTP/1.1"), "ErrInvalidHeaderName")
invalid("hdr-empty-name", raw("GET / HTTP/1.1", H, ": v"), "ErrInvalidHeaderName")
invalid("hdr-colon-only", raw("GET / HTTP/1.1", H, ":"), "ErrInvalidHeaderName")
invalid("hdr-name-with-space", raw("GET / HTTP/1.1", H, "Bad Name: v"), "ErrInvalidHeaderName")
invalid("hdr-name-with-paren", raw("GET / HTTP/1.1", H, "Bad(Name): v"), "ErrInvalidHeaderName")
invalid("hdr-name-with-slash", raw("GET / HTTP/1.1", H, "Bad/Name: v"), "ErrInvalidHeaderName")
invalid("hdr-name-non-ascii", raw("GET / HTTP/1.1", H, "X-\xe9: v"), "ErrInvalidHeaderName")
invalid("hdr-name-nul", raw("GET / HTTP/1.1", H, "X\x00Y: v"), "ErrInvalidHeaderName")
for label, byte in [("nul", 0x00), ("soh", 0x01), ("vt", 0x0b), ("ff", 0x0c), ("esc", 0x1b), ("del", 0x7f)]:
    invalid("hdr-value-control-%s" % label, raw("GET / HTTP/1.1", H, "X: a" + chr(byte) + "b"), "ErrInvalidHeaderValue")
# 100 header lines (including Host) are allowed, 101 are not.
valid("limit-headers-exactly-100", raw("GET / HTTP/1.1", H, *["X-N%d: v" % i for i in range(99)]), E())
invalid("limit-headers-101", raw("GET / HTTP/1.1", H, *["X-N%d: v" % i for i in range(100)]), "ErrTooManyHeaders")
invalid("limit-headers-3000-tiny", raw("GET / HTTP/1.1", H, *["a: b"] * 3000), "ErrTooManyHeaders")
# The header section (request line, all headers, final CRLF) is limited to 16384 bytes.
def sized_head(total):
    base = len(b"GET / HTTP/1.1\r\n") + len(b"Host: h\r\n") + len(b"X: ") + len(b"\r\n") + len(b"\r\n")
    return raw("GET / HTTP/1.1", H, "X: " + "a" * (total - base))
valid("limit-headers-at-16384-bytes", sized_head(16384), E(headers={"X": ["a" * (16384 - 32)]}))
invalid("limit-headers-at-16385-bytes", sized_head(16385), "ErrHeaderTooLarge")
invalid("limit-headers-one-huge-line", raw("GET / HTTP/1.1", H, "X: " + "a" * 100000), "ErrHeaderTooLarge")

# ------------------------------------------------------- framing / smuggling
invalid("smuggle-cl-then-te", raw("POST / HTTP/1.1", H, "Content-Length: 5", TE, body=b"hello"), "ErrContentLengthWithChunked")
invalid("smuggle-te-then-cl", raw("POST / HTTP/1.1", H, TE, "Content-Length: 5", body=b"hello"), "ErrContentLengthWithChunked")
invalid("smuggle-classic-cl-te", raw("POST / HTTP/1.1", H, "Content-Length: 4", TE, body=b"1\r\nZ\r\nQ"), "ErrContentLengthWithChunked")
invalid("smuggle-te-space-before-colon", raw("POST / HTTP/1.1", H, "Content-Length: 5", "Transfer-Encoding : chunked"), "ErrSpaceBeforeColon")
invalid("smuggle-te-vertical-tab", raw("POST / HTTP/1.1", H, "Transfer-Encoding: \x0bchunked"), "ErrInvalidHeaderValue")
invalid("smuggle-te-xchunked-with-cl", raw("POST / HTTP/1.1", H, "Content-Length: 5", "Transfer-Encoding: xchunked"), "ErrContentLengthWithChunked")
invalid("smuggle-te-xchunked", raw("POST / HTTP/1.1", H, "Transfer-Encoding: xchunked"), "ErrUnsupportedTransferEncoding")
invalid("smuggle-te-chunked-then-identity", raw("POST / HTTP/1.1", H, "Transfer-Encoding: chunked, identity"), "ErrUnsupportedTransferEncoding")
invalid("smuggle-te-two-headers", raw("POST / HTTP/1.1", H, TE, "Transfer-Encoding: identity"), "ErrUnsupportedTransferEncoding")
invalid("smuggle-te-chunked-twice", raw("POST / HTTP/1.1", H, "Transfer-Encoding: chunked, chunked"), "ErrUnsupportedTransferEncoding")
invalid("smuggle-te-trailing-comma", raw("POST / HTTP/1.1", H, "Transfer-Encoding: chunked,"), "ErrUnsupportedTransferEncoding")
invalid("smuggle-te-empty", raw("POST / HTTP/1.1", H, "Transfer-Encoding:"), "ErrUnsupportedTransferEncoding")
invalid("smuggle-te-gzip", raw("POST / HTTP/1.1", H, "Transfer-Encoding: gzip"), "ErrUnsupportedTransferEncoding")
invalid("smuggle-te-gzip-then-chunked", raw("POST / HTTP/1.1", H, "Transfer-Encoding: gzip, chunked"), "ErrUnsupportedTransferEncoding")
invalid("smuggle-te-identity", raw("POST / HTTP/1.1", H, "Transfer-Encoding: identity"), "ErrUnsupportedTransferEncoding")
for label, v, err in [
    ("letters", "5a", "ErrInvalidContentLength"), ("negative", "-5", "ErrInvalidContentLength"),
    ("plus", "+5", "ErrInvalidContentLength"), ("empty", "", "ErrInvalidContentLength"),
    ("hex", "0x10", "ErrInvalidContentLength"), ("float", "5.0", "ErrInvalidContentLength"),
    ("inner-space", "5 5", "ErrInvalidContentLength"), ("quoted", '"5"', "ErrInvalidContentLength"),
    ("trailing-comma", "5,", "ErrInvalidContentLength"), ("leading-comma", ",5", "ErrInvalidContentLength"),
    ("arabic-indic-digit", "\xd9\xa5", "ErrInvalidContentLength"),
    ("overflow-int64", "9223372036854775808", "ErrInvalidContentLength"),
    ("overflow-huge", "99999999999999999999", "ErrInvalidContentLength"),
    ("different-values-in-list", "5, 6", "ErrConflictingContentLength"),
    ("three-values-last-differs", "5,5,6", "ErrConflictingContentLength"),
    ("int64-max", "9223372036854775807", "ErrBodyTooLarge"),
    ("just-over-1mib", "1048577", "ErrBodyTooLarge"),
]:
    invalid("cl-%s" % label, raw("POST / HTTP/1.1", H, "Content-Length: " + v), err)
invalid("cl-two-different-headers", raw("POST / HTTP/1.1", H, "Content-Length: 5", "Content-Length: 6", body=b"hello"), "ErrConflictingContentLength")
invalid("cl-body-truncated", raw("POST / HTTP/1.1", H, "Content-Length: 10", body=b"abc"), "ErrTruncatedBody")
invalid("cl-body-missing", raw("POST / HTTP/1.1", H, "Content-Length: 5"), "ErrTruncatedBody")

# -------------------------------------------------------------- chunked body
def bad_chunked(name, body_bytes, err):
    invalid("chunk-" + name, post_chunked(body_bytes), err)
bad_chunked("size-not-hex", b"zz\r\nhi\r\n0\r\n\r\n", "ErrInvalidChunkSize")
bad_chunked("size-empty", b"\r\nhi\r\n0\r\n\r\n", "ErrInvalidChunkSize")
bad_chunked("size-extension-only", b";x\r\nhi\r\n0\r\n\r\n", "ErrInvalidChunkSize")
bad_chunked("size-leading-space", b" 5\r\nhello\r\n0\r\n\r\n", "ErrInvalidChunkSize")
bad_chunked("size-negative", b"-5\r\nhello\r\n0\r\n\r\n", "ErrInvalidChunkSize")
bad_chunked("size-plus", b"+5\r\nhello\r\n0\r\n\r\n", "ErrInvalidChunkSize")
bad_chunked("size-0x-prefix", b"0x5\r\nhello\r\n0\r\n\r\n", "ErrInvalidChunkSize")
bad_chunked("size-overflow", b"ffffffffffffffffff\r\nx\r\n0\r\n\r\n", "ErrInvalidChunkSize")
bad_chunked("size-bare-cr", b"5\rx\r\nhello\r\n0\r\n\r\n", "ErrInvalidChunkSize")
bad_chunked("extension-control-char", b"5;a\x00b\r\nhello\r\n0\r\n\r\n", "ErrMalformedChunk")
bad_chunked("size-line-bare-lf", b"5\nhello\r\n0\r\n\r\n", "ErrBareLF")
bad_chunked("missing-crlf-after-data", b"5\r\nhelloXX0\r\n\r\n", "ErrMalformedChunk")
bad_chunked("lf-only-after-data", b"5\r\nhello\n0\r\n\r\n", "ErrMalformedChunk")
bad_chunked("data-longer-than-size", b"3\r\nhello\r\n0\r\n\r\n", "ErrMalformedChunk")
bad_chunked("data-shorter-than-size", b"7\r\nhello\r\n0\r\n\r\n", "ErrMalformedChunk")
bad_chunked("truncated-in-size-line", b"5", "ErrTruncatedBody")
bad_chunked("truncated-in-data", b"5\r\nhel", "ErrTruncatedBody")
bad_chunked("truncated-before-data-crlf", b"5\r\nhello", "ErrTruncatedBody")
bad_chunked("truncated-no-last-chunk", b"5\r\nhello\r\n", "ErrTruncatedBody")
bad_chunked("truncated-after-zero-chunk", b"0\r\n", "ErrTruncatedBody")
bad_chunked("truncated-in-trailers", b"0\r\nX: 1\r\n", "ErrTruncatedBody")
bad_chunked("body-missing-entirely", b"", "ErrTruncatedBody")
bad_chunked("trailer-invalid-name", b"0\r\nbad trailer\r\n\r\n", "ErrInvalidHeaderName")
bad_chunked("trailer-obs-fold", b"0\r\nA: 1\r\n b\r\n\r\n", "ErrObsFold")
bad_chunked("trailer-bare-lf", b"0\r\nA: 1\n\r\n", "ErrBareLF")
bad_chunked("trailer-control-char", b"0\r\nA: a\x00b\r\n\r\n", "ErrInvalidHeaderValue")
bad_chunked("too-many-trailers", b"0\r\n" + b"".join(b"T%d: v\r\n" % i for i in range(101)) + b"\r\n", "ErrTooManyHeaders")
bad_chunked("line-over-limit", b"5;" + b"a" * (1024 - 2 - 2 + 1) + b"\r\nhello\r\n0\r\n\r\n", "ErrChunkLineTooLong")
bad_chunked("line-way-over-limit", b"5;" + b"a" * 5000 + b"\r\nhello\r\n0\r\n\r\n", "ErrChunkLineTooLong")
bad_chunked("body-over-1mib", chunked([b1m[:400 * 1024]] * 3), "ErrBodyTooLarge")
bad_chunked("one-byte-over-1mib", chunked([b1m, b"x"]), "ErrBodyTooLarge")
bad_chunked("giant-announced-size", b"7fffffffffffffff\r\n", "ErrBodyTooLarge")

# ------------------------------------------------------------ truncated heads
invalid("trunc-mid-request-line", b"GET / HT", "io.ErrUnexpectedEOF")
invalid("trunc-after-request-line", b"GET / HTTP/1.1\r\n", "io.ErrUnexpectedEOF")
invalid("trunc-mid-header", b"GET / HTTP/1.1\r\nHost: h\r\nX: partia", "io.ErrUnexpectedEOF")
invalid("trunc-no-final-blank-line", b"GET / HTTP/1.1\r\nHost: h\r\n", "io.ErrUnexpectedEOF")
invalid("trunc-single-cr-at-end", b"GET / HTTP/1.1\r\nHost: h\r\n\r", "io.ErrUnexpectedEOF")
invalid("trunc-second-request", GET + b"GET /b HT", "io.ErrUnexpectedEOF", E())

# ------------------------------------------------------- errors mid-pipeline
invalid("pipeline-second-has-two-hosts", GET + raw("GET /b HTTP/1.1", "Host: a", "Host: b"), "ErrMultipleHost", E())
invalid("pipeline-second-smuggles-te", GET + raw("POST /b HTTP/1.1", H, "Content-Length: 3", TE, body=b"abc"), "ErrContentLengthWithChunked", E())
invalid("pipeline-second-bad-version", GET + raw("GET /b HTTP/2.0", H), "ErrUnsupportedVersion", E())
invalid("pipeline-second-bare-lf", GET + b"GET /b HTTP/1.1\nHost: h\n\n", "ErrBareLF", E())
invalid("pipeline-first-body-truncated", raw("POST / HTTP/1.1", H, "Content-Length: 50", body=b"short"), "ErrTruncatedBody")

# ------------------------------------------------------------------- output
def main():
    for sub in ("valid", "invalid"):
        shutil.rmtree(os.path.join(HERE, sub), ignore_errors=True)
        os.makedirs(os.path.join(HERE, sub))
    manifest = {}
    for path, (data, expect) in sorted(files.items()):
        with open(os.path.join(HERE, path), "wb") as f:
            f.write(data)
        manifest[path] = expect
    with open(os.path.join(HERE, "expect.json"), "w") as f:
        json.dump(manifest, f, indent=1, sort_keys=True)
        f.write("\n")
    nv = sum(1 for p in files if p.startswith("valid/"))
    print("wrote %d files (%d valid, %d invalid) and expect.json" % (len(files), nv, len(files) - nv))

main()
