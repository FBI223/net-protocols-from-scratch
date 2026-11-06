# RADIUS

**Specification:** RFC 2865, 2866  
**Verify with:** radtest, eapol_test

## What this is

The authentication and accounting protocol of ISPs and Wi-Fi networks.

## What to build

- Access-Request, Access-Accept and Access-Reject
- Attribute parsing, including vendor-specific attributes
- Request authenticator and Message-Authenticator
- Accounting-Request with Start, Interim and Stop
- Subscribers stored in a database
- Test with radtest and eapol_test

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
