package discovery

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"
)

// Punching is split deterministically by role — sender dials, receiver
// accepts — specifically to avoid a real bug an earlier symmetric
// dial+accept design had: on a permissive network both directions can
// succeed independently, letting each side latch onto a *different*
// physical connection. This test proves both sides consistently land on
// the same one by exchanging bytes end-to-end, not just that each side
// gets *a* connection.
func TestPunchDirectConnectsOverLoopback(t *testing.T) {
	receiverLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("receiver listen: %v", err)
	}
	defer receiverLn.Close()
	receiverPort := receiverLn.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var senderConn, receiverConn net.Conn
	var senderErr, receiverErr error
	done := make(chan struct{}, 2)
	go func() {
		senderConn, senderErr = PunchDirect(ctx, RoleSender, nil, PeerInfo{IP: "127.0.0.1", PunchPort: receiverPort})
		done <- struct{}{}
	}()
	go func() {
		receiverConn, receiverErr = PunchDirect(ctx, RoleReceiver, receiverLn, PeerInfo{})
		done <- struct{}{}
	}()
	<-done
	<-done
	if senderErr != nil {
		t.Fatalf("sender PunchDirect: %v", senderErr)
	}
	if receiverErr != nil {
		t.Fatalf("receiver PunchDirect: %v", receiverErr)
	}
	defer senderConn.Close()
	defer receiverConn.Close()

	msg := []byte("direct-punched-bytes")
	go senderConn.Write(msg)
	buf := make([]byte, len(msg))
	receiverConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(receiverConn, buf); err != nil {
		t.Fatalf("receiver read: %v", err)
	}
	if !bytes.Equal(buf, msg) {
		t.Errorf("got %q, want %q", buf, msg)
	}
}

func TestPunchDirectSenderRejectsNoOffer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := PunchDirect(ctx, RoleSender, nil, PeerInfo{})
	if err != ErrNoPunchTarget {
		t.Errorf("expected ErrNoPunchTarget, got %v", err)
	}
}

func TestPunchDirectReceiverRejectsNilListener(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := PunchDirect(ctx, RoleReceiver, nil, PeerInfo{})
	if err != ErrNoPunchTarget {
		t.Errorf("expected ErrNoPunchTarget, got %v", err)
	}
}

// Proves the relay's pairing handshake actually delivers usable peer info
// end-to-end (each side learns the other's loopback address and
// advertised port) and that PunchDirect, fed exactly that info with the
// correct roles, connects directly — the same path cmd/parcel's
// connectViaRelayOrPunch drives.
func TestRelayPairingDeliversUsablePeerInfo(t *testing.T) {
	addr, _ := startTestRelay(t)

	receiverLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("receiver listen: %v", err)
	}
	defer receiverLn.Close()
	receiverPort := receiverLn.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var senderPeer, receiverPeer PeerInfo
	var senderRelayConn, receiverRelayConn net.Conn
	var senderErr, receiverErr error
	done := make(chan struct{}, 2)
	go func() {
		// Sender doesn't need its own listener in the role-split design
		// (it only dials), so it offers punch port 0.
		senderRelayConn, senderPeer, senderErr = DialRelay(ctx, addr, "punch-info-code", RoleSender, 0)
		done <- struct{}{}
	}()
	go func() {
		receiverRelayConn, receiverPeer, receiverErr = DialRelay(ctx, addr, "punch-info-code", RoleReceiver, receiverPort)
		done <- struct{}{}
	}()
	<-done
	<-done
	if senderErr != nil {
		t.Fatalf("sender dial: %v", senderErr)
	}
	if receiverErr != nil {
		t.Fatalf("receiver dial: %v", receiverErr)
	}
	defer senderRelayConn.Close()
	defer receiverRelayConn.Close()

	if senderPeer.PunchPort != receiverPort {
		t.Errorf("sender learned peer port %d, want %d", senderPeer.PunchPort, receiverPort)
	}
	if senderPeer.IP != "127.0.0.1" {
		t.Errorf("expected loopback peer IP, got %q", senderPeer.IP)
	}
	_ = receiverPeer // receiver's role branch doesn't need sender's info

	punchCtx, punchCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer punchCancel()

	var direct, accepted net.Conn
	var directErr, acceptedErr error
	done2 := make(chan struct{}, 2)
	go func() {
		direct, directErr = PunchDirect(punchCtx, RoleSender, nil, senderPeer)
		done2 <- struct{}{}
	}()
	go func() {
		accepted, acceptedErr = PunchDirect(punchCtx, RoleReceiver, receiverLn, PeerInfo{})
		done2 <- struct{}{}
	}()
	<-done2
	<-done2
	if directErr != nil {
		t.Fatalf("sender PunchDirect using relay-delivered peer info: %v", directErr)
	}
	if acceptedErr != nil {
		t.Fatalf("receiver PunchDirect: %v", acceptedErr)
	}
	defer direct.Close()
	defer accepted.Close()

	msg := []byte("direct-punched-bytes")
	go direct.Write(msg)
	buf := make([]byte, len(msg))
	accepted.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(accepted, buf); err != nil {
		t.Fatalf("read punched bytes: %v", err)
	}
	if !bytes.Equal(buf, msg) {
		t.Errorf("got %q, want %q", buf, msg)
	}
}
