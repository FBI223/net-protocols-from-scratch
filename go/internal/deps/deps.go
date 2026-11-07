// Package deps pins the external modules this repository will need, so that
// they stay in go.mod and go.sum until real code imports them.
//
// Delete this package once every import below is used by a protocol package.
package deps

import (
	_ "golang.org/x/crypto/blake2s"
	_ "golang.org/x/crypto/chacha20poly1305"
	_ "golang.org/x/crypto/curve25519"
	_ "golang.org/x/crypto/ssh"
	_ "golang.org/x/net/icmp"
	_ "golang.org/x/net/ipv4"
	_ "golang.org/x/sys/unix"
	_ "google.golang.org/protobuf/proto"
)
