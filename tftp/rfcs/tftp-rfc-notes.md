# TFTP: RFC notes

Own-words notes for `docs/rfc-notes/tftp.md`. Written 2026-11-08.

## What was actually read

| RFC | How it was read |
|---|---|
| 1350 (TFTP rev. 2) | Full text. Errata page NOT read: check https://errata.rfc-editor.org/search/?rfc_number=1350 |
| 2348 (blksize) | Full text. |
| 2347 (option extension) | Only excerpts (OACK format, error code 8, examples, server/client rules). |
| 2349 (timeout, tsize) | Only excerpts. |
| 1123 §4.2.3.1 and §4.2.3.2 | Excerpts via a mirror (freesoft.org), not the RFC Editor copy. |

Treat the section numbers below as checked against RFC 1350 and RFC 1123 only.

## RFC 1350

- §2 Overview: lock-step. Every DATA is acknowledged before the next is sent.
  Data field is 0..512 bytes; fewer than 512 ends the transfer. A lost packet
  is handled by timeout plus resending the last packet. Only one packet has to
  be kept for resending.
- §2: ERROR is not acknowledged and not retransmitted. Timeouts detect a lost
  ERROR. The only error that does NOT end the transfer is a packet with the
  wrong source port.
- §3: TIDs are UDP ports (0..65535).
- §4 Initial connection: client picks a random TID, sends the request to port
  69. Server answers from its own random TID. Both sides then check the source
  TID of every packet. A packet from another TID gets an ERROR, and the real
  transfer is not disturbed.
- §4: block numbers start at 1. The answer to a WRQ is an ACK with block 0.
  The answer to an RRQ is DATA block 1.
- §5 Packets: opcodes 1 RRQ, 2 WRQ, 3 DATA, 4 ACK, 5 ERROR.
  RRQ/WRQ = opcode, filename, 0, mode, 0. Mode is netascii, octet or mail in
  any mix of upper and lower case. Mail is obsolete and should not be
  implemented. Filename and error message are netascii, zero terminated.
- §6 Normal termination: last DATA is 0..511 bytes (datagram < 516). The
  acknowledger is encouraged to "dally": wait a while after the final ACK so it
  can resend it if the final DATA arrives again.
- §7 Premature termination: ERROR is only a courtesy.
- Appendix, Error Codes: 0 not defined, 1 file not found, 2 access violation,
  3 disk full, 4 illegal operation, 5 unknown TID, 6 file exists, 7 no such
  user. NOTE: the table is in the Appendix, not in §5 (earlier notes said §5).
- Security Considerations: no login, no access control. Restrict what the
  server process may touch. Path traversal is the implementation's problem.
- Not in the RFC: any limit on request length, any timeout value, what happens
  when the 16-bit block number wraps.

## RFC 2347 (options)

- Options are appended to RRQ/WRQ as name, 0, value, 0 pairs. Names and values
  are case-insensitive ASCII.
- New packet OACK, opcode 6: pairs name/value, only options the server accepts.
- New error code 8: transfer ended because of option negotiation.
- Server rules: only the client can start negotiation. Unsupported options are
  omitted from the OACK and must not cause an ERROR. If none are accepted, the
  server answers like a plain request.
- RRQ with options: server sends OACK, client confirms with ACK block 0, then
  DATA 1.
- WRQ with options: server sends OACK, client starts with DATA 1.
- Client receiving an OACK with an option it never asked for: ERROR code 8.
- Verified erratum EID 1258 fixes a mislabeled example (RRQ vs WRQ).

## RFC 2348 (blksize)

- Option "blksize", value in ASCII, valid 8..65464 octets. Counts data octets
  only (not the 4-byte header).
- Server may answer with a smaller value than requested, never larger. Client
  accepts it or ends the transfer with ERROR 8.
- Final packet rule unchanged: a DATA shorter than the negotiated size ends
  the transfer. If the file size is an exact multiple, an extra empty DATA is
  sent.
- Bigger than the path MTU causes IP fragmentation and extra overhead.
- Implication for the code: `MaxPayload = 512` must become a per-transfer
  value; `ParseData` and `Data.MarshalBinary` will need a limit parameter.

## RFC 2349 (timeout, tsize)

- "timeout": seconds, 1..255 range per the RFC (not re-read in full). Server
  must answer with the same value the client sent.
- "tsize": file size in octets. In an RRQ the client sends 0 and the server
  returns the real size in the OACK. In a WRQ the client sends the size. A
  side that cannot take the file may abort with ERROR code 3.

## RFC 1123 §4.2.3 (what a real implementation MUST do)

- §4.2.3.1 Sorcerer's Apprentice Syndrome: the sender of DATA must NEVER resend
  the current DATA on receiving a duplicate ACK. Resend only on timeout.
  Otherwise one delayed ACK makes every later packet double.
- §4.2.3.2: a TFTP implementation MUST use an adaptive timeout. At least an
  exponential backoff of the retransmission timeout is necessary.
  NOTE: a plain fixed timeout (earlier suggestion: 1 s, 5 tries) is below this
  bar. Start with fixed, then add backoff before calling it "done".

## Decisions made in this repo (not from the RFCs)

- Request, ERROR and DATA length limit of 516 bytes in parsers: our own limit.
- Duplicate option names in one request: rejected.
- Mode "mail": parsed, then refused by the server with ERROR 4.
- Unknown options: parsed and ignored (server answers as a plain request).
