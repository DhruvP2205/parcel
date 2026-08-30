package discovery

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
)

// The relay's pairing protocol is deliberately tiny: it only exists to
// match a sender and receiver that share a code, tell each one how to try
// reaching the other directly (see punch.go), and then get out of the
// way. Everything that crosses a relay connection after pairing is a raw
// byte stream — the transfer protocol's own AES-GCM framing (internal/
// transfer, internal/crypto) already encrypted it, so the relay operator
// never sees plaintext and never parses anything past this handshake. A
// relay is an untrusted, dumb forwarder: even if it tried to actively
// impersonate a peer, the code-authenticated handshake in internal/crypto
// still fails closed without knowing the code.
const (
	relayMagic = "PRLY"
	maxCodeLen = 255
	// magic + role byte + code-length byte + punch-port (2 bytes)
	relayHeader = len(relayMagic) + 2 + 2
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

// writeRelayHello registers this connection with the relay. punchPort is
// this client's own local listener port, offered so the relay can pass it
// to the matched peer as a hole-punch target; pass 0 to opt out of
// punching entirely (the relay will still work as a plain forwarder).
func writeRelayHello(conn net.Conn, role RelayRole, code string, punchPort int) error {
	if len(code) == 0 || len(code) > maxCodeLen {
		return fmt.Errorf("discovery: invalid relay code length %d", len(code))
	}
	buf := make([]byte, 0, relayHeader+len(code))
	buf = append(buf, relayMagic...)
	buf = append(buf, byte(role))
	buf = append(buf, byte(len(code)))
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(punchPort))
	buf = append(buf, portBytes...)
	buf = append(buf, code...)
	_, err := conn.Write(buf)
	return err
}

func readRelayHello(conn net.Conn) (role RelayRole, code string, punchPort int, err error) {
	header := make([]byte, relayHeader)
	if _, err := io.ReadFull(conn, header); err != nil {
		return 0, "", 0, err
	}
	if string(header[:len(relayMagic)]) != relayMagic {
		return 0, "", 0, errors.New("discovery: bad relay hello magic")
	}
	role = RelayRole(header[len(relayMagic)])
	if role != RoleSender && role != RoleReceiver {
		return 0, "", 0, fmt.Errorf("discovery: bad relay role %d", role)
	}
	codeLen := int(header[len(relayMagic)+1])
	punchPort = int(binary.BigEndian.Uint16(header[len(relayMagic)+2:]))
	codeBytes := make([]byte, codeLen)
	if codeLen > 0 {
		if _, err := io.ReadFull(conn, codeBytes); err != nil {
			return 0, "", 0, err
		}
	}
	return role, string(codeBytes), punchPort, nil
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

// PeerInfo is what the relay tells each side about the other once paired:
// the peer's public IP as observed on the relay's TCP connection, and the
// local listener port the peer offered for hole-punch attempts (0 if it
// didn't offer one).
type PeerInfo struct {
	IP        string
	PunchPort int
}

// writePeerInfo/readPeerInfo frame a small info message: a 1-byte IP
// length, the IP as ASCII, then a 2-byte punch port. Sent by the relay
// immediately after the "paired" status byte, before raw byte forwarding
// begins.
func writePeerInfo(conn net.Conn, info PeerInfo) error {
	if len(info.IP) > 255 {
		return fmt.Errorf("discovery: peer IP too long to frame: %d bytes", len(info.IP))
	}
	buf := make([]byte, 0, 1+len(info.IP)+2)
	buf = append(buf, byte(len(info.IP)))
	buf = append(buf, info.IP...)
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(info.PunchPort))
	buf = append(buf, portBytes...)
	_, err := conn.Write(buf)
	return err
}

func readPeerInfo(conn net.Conn) (PeerInfo, error) {
	lenByte := make([]byte, 1)
	if _, err := io.ReadFull(conn, lenByte); err != nil {
		return PeerInfo{}, err
	}
	rest := make([]byte, int(lenByte[0])+2)
	if _, err := io.ReadFull(conn, rest); err != nil {
		return PeerInfo{}, err
	}
	ip := string(rest[:lenByte[0]])
	port := int(binary.BigEndian.Uint16(rest[lenByte[0]:]))
	return PeerInfo{IP: ip, PunchPort: port}, nil
}

// hostIP extracts the bare IP (no port) from a net.Addr belonging to a TCP
// connection, for reporting a peer's observed public address.
func hostIP(addr net.Addr) string {
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		return ""
	}
	return tcpAddr.IP.String()
}
