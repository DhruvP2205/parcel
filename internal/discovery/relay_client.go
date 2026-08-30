package discovery

import (
	"context"
	"fmt"
	"net"
	"time"
)

// DialRelay connects to a relay server at addr, registers for code with
// the given role, and blocks until a matching peer arrives or ctx is done.
// The returned net.Conn behaves exactly like a direct TCP connection to
// the peer — the caller (internal/transfer) uses it identically to a LAN
// connection, since every byte crossing it is already end-to-end
// encrypted before it reaches here. punchPort is this client's own local
// listener port, offered to the peer as a hole-punch target (see
// punch.go); pass 0 to opt out. The returned PeerInfo describes the
// peer's equivalent offer, for the caller to attempt a punch upgrade.
func DialRelay(ctx context.Context, addr, code string, role RelayRole, punchPort int) (net.Conn, PeerInfo, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, PeerInfo{}, fmt.Errorf("discovery: dial relay %s: %w", addr, err)
	}

	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	} else {
		conn.SetDeadline(time.Now().Add(relayWaitTimeout))
	}

	if err := writeRelayHello(conn, role, code, punchPort); err != nil {
		conn.Close()
		return nil, PeerInfo{}, fmt.Errorf("discovery: relay hello: %w", err)
	}
	status, err := readRelayStatus(conn)
	if err != nil {
		conn.Close()
		return nil, PeerInfo{}, fmt.Errorf("discovery: relay pairing: %w", err)
	}
	if status != relayStatusPaired {
		conn.Close()
		return nil, PeerInfo{}, ErrRelayRejected
	}
	peer, err := readPeerInfo(conn)
	if err != nil {
		conn.Close()
		return nil, PeerInfo{}, fmt.Errorf("discovery: relay peer info: %w", err)
	}
	conn.SetDeadline(time.Time{})

	return conn, peer, nil
}
