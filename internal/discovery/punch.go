package discovery

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"
)

// ErrNoPunchTarget means the peer didn't offer a punch endpoint (it opted
// out, or its own listener failed to start), so there's nothing to try.
var ErrNoPunchTarget = errors.New("discovery: peer did not offer a punch endpoint")

// punchDialInterval is how long to wait between direct-dial retries.
const punchDialInterval = 300 * time.Millisecond

// PunchDirect attempts a direct TCP connection to peer without going
// through the relay: RoleSender repeatedly dials the receiver's
// relay-observed public address and advertised listener port; RoleReceiver
// accepts on the listener it already advertised in the pairing handshake.
//
// An earlier version had both sides simultaneously dial AND accept (true
// TCP simultaneous-open), racing whichever direction succeeded first
// locally. That surfaced a real correctness bug during testing: on a
// permissive network both directions can succeed independently, so each
// side's local race can resolve to a *different* physical connection —
// sender and receiver end up talking on two unrelated sockets instead of
// the same one, and the transfer protocol fails with confusing I/O
// errors. Fixing that properly needs each side's outbound dial bound to
// its own advertised local port so the OS treats both connect() calls as
// halves of one simultaneous open, which behaves inconsistently across
// platforms. Splitting cleanly by role (one dials, one accepts) trades
// away symmetric-NAT- or restrictive-inbound-on-the-receiver scenarios for
// a mechanism that is simple, deterministic, and can't produce a
// sender/receiver mismatch. Whenever it doesn't succeed, the relay
// connection obtained alongside it is always used as a fallback — see
// cmd/parcel's connectViaRelayOrPunch, which only closes the relay leg
// once a punch attempt actually succeeds.
func PunchDirect(ctx context.Context, role RelayRole, ln net.Listener, peer PeerInfo) (net.Conn, error) {
	switch role {
	case RoleSender:
		if peer.PunchPort == 0 || peer.IP == "" {
			return nil, ErrNoPunchTarget
		}
		target := net.JoinHostPort(peer.IP, strconv.Itoa(peer.PunchPort))
		return dialUntil(ctx, target)
	case RoleReceiver:
		if ln == nil {
			return nil, ErrNoPunchTarget
		}
		return acceptUntil(ctx, ln)
	default:
		return nil, fmt.Errorf("discovery: unknown role for punching: %v", role)
	}
}

func dialUntil(ctx context.Context, target string) (net.Conn, error) {
	d := net.Dialer{Timeout: 800 * time.Millisecond}
	for {
		conn, err := d.DialContext(ctx, "tcp", target)
		if err == nil {
			return conn, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(punchDialInterval):
		}
	}
}

func acceptUntil(ctx context.Context, ln net.Listener) (net.Conn, error) {
	connCh := make(chan net.Conn, 1)
	errCh := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			errCh <- err
			return
		}
		connCh <- conn
	}()

	select {
	case conn := <-connCh:
		return conn, nil
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		ln.Close() // unblock the Accept goroutine
		return nil, ctx.Err()
	}
}
