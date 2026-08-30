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
// encrypted before it reaches here.
func DialRelay(ctx context.Context, addr, code string, role RelayRole) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("discovery: dial relay %s: %w", addr, err)
	}

	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	} else {
		conn.SetDeadline(time.Now().Add(relayWaitTimeout))
	}

	if err := writeRelayHello(conn, role, code); err != nil {
		conn.Close()
		return nil, fmt.Errorf("discovery: relay hello: %w", err)
	}
	status, err := readRelayStatus(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("discovery: relay pairing: %w", err)
	}
	conn.SetDeadline(time.Time{})

	if status != relayStatusPaired {
		conn.Close()
		return nil, ErrRelayRejected
	}
	return conn, nil
}
