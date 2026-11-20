# What to read, and in which order

Section numbers are from RFC 9112 unless the RFC is named. 

| Step | Sections | You decide / you learn |
|---|---|---|
| 1. Message format | 2.1, 2.2, 2.3 | CRLF is the line end. A recipient MAY accept a bare LF; a bare CR must be invalid or replaced. The version rule behind 505. |
| 2. Request line | 3, 3.1, 3.2 (origin-form first), 3.3 | The request-line limit: section 3 recommends supporting at least 8000 octets. The `Host` rule (a missing or repeated `Host` gets 400). |
| 3. Fields | 5.1, 5.2; RFC 9110 5.1, 5.5, 5.6 | A field name is a token. No space before the colon. Obsolete folding. Which bytes a value may not contain (CR, LF, NUL). |
| 4. Body length | 6.1, 6.2, **6.3** | The algorithm in 6.3 is the heart of the package: read it until you can write it as a list of cases. |
| 5. Chunked | 7.1, 7.1.1, 7.1.2, 7.1.3 | Chunk size in hex, extensions, trailer section, and how decoding ends. |
| 6. Connections | 9.3 (persistence); RFC 9110 `Connection` field | When a connection stays open, and when it must be closed. |
| 7. Security | 11.1 (response splitting), **11.2 (request smuggling)** | Why every strict choice in `testdata/requests/README.md` exists. |
| 8. Status codes | RFC 9110 15.5.1 (400), 15.5.15 (414), 15.6.6 (505) | 431 is from RFC 6585; read it when you write the header-size limit. |

## Decisions the RFC leaves to you

Record each one here when you take it, with the reason.

| Question | RFC says | Proposal |
|---|---|---|
| Accept a bare LF? | MAY (2.2) | Reject |
| Obsolete line folding | reject, or replace with spaces (5.2) | Reject |
| `Content-Length` together with `Transfer-Encoding` | MAY reject (6.1) | Reject |
| Request-line limit | recommends at least 8000 octets (3) | 8 KiB, in `Limits` |
| Header section limit | no number | decide, put it in `Limits`, test the edge |
| Pipelined requests | allowed (9.3) | decide: serve in order, or close after one |
