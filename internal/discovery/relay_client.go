package discovery

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// relayConnectTimeout bounds how long a single candidate relay address (see
// DialRelayFallback) gets to accept a bare TCP connection before we give up
// on it and try the next one. It only bounds that initial dial — once an
// address accepts the connection, the caller's own context governs how
// long we then wait to be paired with a peer, since a slow pairing wait is
// normal (the other side just hasn't connected yet), not a sign the
// address is bad.
const relayConnectTimeout = 10 * time.Second

// ErrAllRelaysUnreachable means none of the addresses passed to
// DialRelayFallback would even accept a TCP connection within
// relayConnectTimeout each — as opposed to ErrRelayRejected, which means a
// relay was reached but declined to pair.
var ErrAllRelaysUnreachable = errors.New("discovery: no relay address was reachable")

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
	return finishRelayHandshake(ctx, conn, code, role, punchPort)
}

// DialRelayFallback tries each address in addrs in order, giving each one
// up to relayConnectTimeout to accept a bare TCP connection before moving
// to the next. The moment one accepts, that address is committed to for
// the rest of the pairing handshake — it is never abandoned mid-wait.
//
// Every address in the list must be given to both the sender and the
// receiver, in the same order: pairing only happens between clients that
// land on the same relay process (see relayRoom in relay_server.go), so if
// the two sides picked different reachable addresses from an inconsistent
// list, each would connect successfully but never find each other.
func DialRelayFallback(ctx context.Context, addrs []string, code string, role RelayRole, punchPort int) (net.Conn, PeerInfo, error) {
	if len(addrs) == 0 {
		return nil, PeerInfo{}, fmt.Errorf("%w: no relay address configured", ErrAllRelaysUnreachable)
	}

	var attempts []string
	for _, addr := range addrs {
		dialCtx, cancel := context.WithTimeout(ctx, relayConnectTimeout)
		var d net.Dialer
		conn, err := d.DialContext(dialCtx, "tcp", addr)
		cancel()
		if err != nil {
			attempts = append(attempts, fmt.Sprintf("%s: %v", addr, err))
			if ctx.Err() != nil {
				break
			}
			continue
		}
		return finishRelayHandshake(ctx, conn, code, role, punchPort)
	}
	return nil, PeerInfo{}, fmt.Errorf("%w (%s)", ErrAllRelaysUnreachable, strings.Join(attempts, "; "))
}

// finishRelayHandshake runs the pairing protocol over an already-dialed
// relay connection: register, wait for a match, read the paired peer's
// info. Shared by DialRelay and DialRelayFallback once a candidate address
// has accepted a connection.
func finishRelayHandshake(ctx context.Context, conn net.Conn, code string, role RelayRole, punchPort int) (net.Conn, PeerInfo, error) {
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
