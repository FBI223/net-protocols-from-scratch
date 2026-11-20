# Raw requests for the parser tests

Each file holds the exact bytes a client sends, CRLF line ends included (open
one with `xxd` to see them). A test reads a file, feeds it to the parser, and
compares the outcome with the table below. The same files can be sent to a
running server with `nc`:

```bash
nc 127.0.0.1 8080 < testdata/requests/valid/get-simple.raw
```

"Decision" means the RFC lets the implementation choose. This package chooses
the strict side everywhere, because a lenient parser is how request smuggling
starts (RFC 9112 section 11.2). A decision can be changed, but the test and the
README must change with it.

The extension is `.raw` on purpose. JetBrains IDEs treat `.http` as their own
HTTP Client format and report errors on lines such as `GET / HTTP/2.0`. Do not
save these files from an editor that normalizes line ends: a file here may
contain a bare LF, a bare CR or a NUL byte on purpose. `.gitattributes` in this
folder tells Git not to touch them.

## valid/

| File | Must give |
|---|---|
| `get-simple.raw` | request `GET /hello`, no body |
| `post-content-length.raw` | body `hello world` (11 bytes) |
| `post-chunked.raw` | body `hello world` from two chunks |
| `header-case-and-spaces.raw` | name `Host` found as `host`; value without the surrounding spaces; `X-Multi` has two values, in order |
| `connection-close.raw` | request accepted; the connection must be closed after the response |

## invalid/

| File | Expected | Rule |
|---|---|---|
| `no-host.raw` | 400 | HTTP/1.1 request without `Host` (RFC 9112 section 3.2) |
| `two-host.raw` | 400 | more than one `Host` (section 3.2) |
| `space-before-colon.raw` | 400 | no whitespace between field name and colon (section 5.1) |
| `obs-fold.raw` | 400 | obsolete line folding (section 5.2). Decision: reject, not replace with spaces |
| `bare-lf.raw` | 400 | a bare LF line end (section 2.2 allows accepting it). Decision: reject |
| `bare-cr.raw` | 400 | a bare CR must be invalid or replaced (section 2.2). Decision: reject |
| `nul-in-header-value.raw` | 400 | NUL in a field value (RFC 9110 section 5.5) |
| `content-length-and-chunked.raw` | 400 | both framings together (sections 6.1, 6.3 and 11.2). Decision: reject |
| `content-length-two-values.raw` | 400 | repeated `Content-Length` with different values (section 6.3) |
| `content-length-not-digits.raw` | 400 | `Content-Length: +5` is not 1*DIGIT (section 6.2) |
| `http-version-2.raw` | 505 | unsupported major version (RFC 9110 section 15.6.6) |
| `chunk-size-not-hex.raw` | 400 | chunk size is hexadecimal (section 7.1) |
| `chunked-truncated.raw` | error from the body read | the stream ends inside a chunk; the connection is closed and no response is sent |

Not stored as files, because they are large or depend on the limits: a request
line over the limit (414), a header section over the limit (431), too many
fields, a chunk size that would need a huge buffer. The tests generate them
from `Limits`.
