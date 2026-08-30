package discovery

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// relayHelloTimeout bounds how long a freshly accepted connection has to
// send its pairing hello before the relay gives up on it.
const relayHelloTimeout = 10 * time.Second

// relayWaitTimeout bounds how long a registered peer waits for its match
// before the relay drops it. Mirrors transfer.SessionWindow (a code should
// stay claimable for as long as it's advertised as valid) without this
// package importing transfer for a single constant.
const relayWaitTimeout = 5 * time.Minute

// RunRelay listens on addr and relays bytes between a sender and receiver
// that connect with the same pairing code. It never sees file content or
// keys — see relay_protocol.go for the trust model. Intended to be run as
// `parcel relay` on a host reachable from both peers, for transfers that
// aren't on the same local network and where LAN discovery can't apply.
func RunRelay(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("discovery: relay listen: %w", err)
	}
	return ServeRelay(ctx, ln)
}

// ServeRelay runs the relay's accept loop against an already-created
// listener. Split out from RunRelay so tests can listen on an ephemeral
// port and learn the real address before serving.
func ServeRelay(ctx context.Context, ln net.Listener) error {
	defer ln.Close()
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	room := newRelayRoom()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
		go handleRelayConn(ctx, conn, room)
	}
}

func handleRelayConn(ctx context.Context, conn net.Conn, room *relayRoom) {
	conn.SetDeadline(time.Now().Add(relayHelloTimeout))
	role, code, punchPort, err := readRelayHello(conn)
	if err != nil {
		conn.Close()
		return
	}
	conn.SetDeadline(time.Time{})

	match, iAmMatcher, err := room.pairOrWait(ctx, code, role, conn, punchPort)
	if err != nil {
		writeRelayStatus(conn, relayStatusReject)
		conn.Close()
		return
	}
	if !iAmMatcher {
		// This goroutine registered and waited; the goroutine that matched
		// us owns both connections now (it received our conn as its peer)
		// and is responsible for status + peer-info + splicing. Nothing
		// left to do here — importantly, do not touch or close conn.
		return
	}

	peer := match.conn
	writeRelayStatus(conn, relayStatusPaired)
	writeRelayStatus(peer, relayStatusPaired)
	writePeerInfo(conn, PeerInfo{IP: hostIP(peer.RemoteAddr()), PunchPort: match.punchPort})
	writePeerInfo(peer, PeerInfo{IP: hostIP(conn.RemoteAddr()), PunchPort: punchPort})
	splice(conn, peer)
}

// splice pipes bytes bidirectionally between two connections until either
// side closes, then closes both.
func splice(a, b net.Conn) {
	defer a.Close()
	defer b.Close()
	done := make(chan struct{}, 2)
	go func() { io.Copy(a, b); done <- struct{}{} }()
	go func() { io.Copy(b, a); done <- struct{}{} }()
	<-done
}

type relayMatch struct {
	conn      net.Conn
	punchPort int
}

type waitingPeer struct {
	conn      net.Conn
	punchPort int
	matched   chan relayMatch
}

// relayRoom pairs a sender and receiver that register with the same code.
// Matching and channel delivery happen under the same lock (see
// pairOrWait) so a waiter's timeout path can never race a matcher that has
// already committed to pairing it.
type relayRoom struct {
	mu      sync.Mutex
	waiting map[string]*waitingPeer
}

func newRelayRoom() *relayRoom {
	return &relayRoom{waiting: make(map[string]*waitingPeer)}
}

func roomKey(code string, role RelayRole) string {
	return fmt.Sprintf("%s\x00%d", code, role)
}

// pairOrWait either immediately pairs conn with an already-waiting
// complementary peer (returning iAmMatcher=true, so the caller drives
// splicing), or registers conn as the waiter and blocks until someone
// pairs with it (iAmMatcher=false) or it times out.
func (r *relayRoom) pairOrWait(ctx context.Context, code string, role RelayRole, conn net.Conn, punchPort int) (match relayMatch, iAmMatcher bool, err error) {
	otherRole := RoleReceiver
	if role == RoleReceiver {
		otherRole = RoleSender
	}
	otherKey := roomKey(code, otherRole)
	myKey := roomKey(code, role)

	r.mu.Lock()
	if other, ok := r.waiting[otherKey]; ok {
		delete(r.waiting, otherKey)
		other.matched <- relayMatch{conn: conn, punchPort: punchPort} // buffered(1): never blocks
		r.mu.Unlock()
		return relayMatch{conn: other.conn, punchPort: other.punchPort}, true, nil
	}
	if _, exists := r.waiting[myKey]; exists {
		r.mu.Unlock()
		return relayMatch{}, false, ErrRelayRejected
	}
	me := &waitingPeer{conn: conn, punchPort: punchPort, matched: make(chan relayMatch, 1)}
	r.waiting[myKey] = me
	r.mu.Unlock()

	select {
	case m := <-me.matched:
		return m, false, nil
	case <-time.After(relayWaitTimeout):
		r.mu.Lock()
		if r.waiting[myKey] == me {
			delete(r.waiting, myKey)
			r.mu.Unlock()
			return relayMatch{}, false, fmt.Errorf("discovery: relay wait timed out for code")
		}
		r.mu.Unlock()
		// A matcher grabbed us right at the deadline and already sent
		// under the lock above — the value is guaranteed to be there.
		m := <-me.matched
		return m, false, nil
	case <-ctx.Done():
		r.mu.Lock()
		if r.waiting[myKey] == me {
			delete(r.waiting, myKey)
			r.mu.Unlock()
			return relayMatch{}, false, ctx.Err()
		}
		r.mu.Unlock()
		m := <-me.matched
		return m, false, nil
	}
}
