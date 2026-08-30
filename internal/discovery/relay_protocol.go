package discovery

import (
	"errors"
	"fmt"
	"io"
	"net"
)

// The relay's pairing protocol is deliberately tiny: it only exists to
// match a sender and receiver that share a code and then get out of the
// way. Everything that crosses a relay connection after pairing is a raw
// byte stream — the transfer protocol's own AES-GCM framing (internal/
// transfer, internal/crypto) already encrypted it, so the relay operator
// never sees plaintext and never parses anything past this handshake. A
// relay is an untrusted, dumb forwarder: even if it tried to actively
// impersonate a peer, the code-authenticated handshake in internal/crypto
// still fails closed without knowing the code.
const (
	relayMagic  = "PRLY"
	maxCodeLen  = 255
	relayHeader = len(relayMagic) + 2 // magic + role byte + code-length byte
)

// RelayRole identifies which side of a transfer a relay client is.
type RelayRole byte

const (
	RoleSender   RelayRole = 0
	RoleReceiver RelayRole = 1
)

const (
	relayStatusPaired byte = 0x01
	relayStatusReject byte = 0x02
)

// ErrRelayRejected means the relay already has a waiting peer registered
// for this exact code+role (e.g. two senders raced to register for the
// same code) — the code is meant to pair exactly one sender with one
// receiver.
var ErrRelayRejected = errors.New("discovery: relay rejected pairing (a peer with this code and role is already waiting)")

func writeRelayHello(conn net.Conn, role RelayRole, code string) error {
	if len(code) == 0 || len(code) > maxCodeLen {
		return fmt.Errorf("discovery: invalid relay code length %d", len(code))
	}
	buf := make([]byte, 0, relayHeader+len(code))
	buf = append(buf, relayMagic...)
	buf = append(buf, byte(role))
	buf = append(buf, byte(len(code)))
	buf = append(buf, code...)
	_, err := conn.Write(buf)
	return err
}

func readRelayHello(conn net.Conn) (RelayRole, string, error) {
	header := make([]byte, relayHeader)
	if _, err := io.ReadFull(conn, header); err != nil {
		return 0, "", err
	}
	if string(header[:len(relayMagic)]) != relayMagic {
		return 0, "", errors.New("discovery: bad relay hello magic")
	}
	role := RelayRole(header[len(relayMagic)])
	if role != RoleSender && role != RoleReceiver {
		return 0, "", fmt.Errorf("discovery: bad relay role %d", role)
	}
	codeLen := int(header[len(relayMagic)+1])
	code := make([]byte, codeLen)
	if codeLen > 0 {
		if _, err := io.ReadFull(conn, code); err != nil {
			return 0, "", err
		}
	}
	return role, string(code), nil
}

func writeRelayStatus(conn net.Conn, status byte) error {
	_, err := conn.Write([]byte{status})
	return err
}

func readRelayStatus(conn net.Conn) (byte, error) {
	b := make([]byte, 1)
	if _, err := io.ReadFull(conn, b); err != nil {
		return 0, err
	}
	return b[0], nil
}
