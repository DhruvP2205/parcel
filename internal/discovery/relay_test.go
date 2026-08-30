package discovery

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func startTestRelay(t *testing.T) (addr string, cancel func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancelCtx := context.WithCancel(context.Background())
	go ServeRelay(ctx, ln)
	t.Cleanup(cancelCtx)
	return ln.Addr().String(), cancelCtx
}

func TestRelayPairsSenderAndReceiverAndForwardsBytesBothWays(t *testing.T) {
	addr, _ := startTestRelay(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var senderConn, receiverConn net.Conn
	var senderErr, receiverErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		senderConn, senderErr = DialRelay(ctx, addr, "test-code-1", RoleSender)
	}()
	go func() {
		defer wg.Done()
		receiverConn, receiverErr = DialRelay(ctx, addr, "test-code-1", RoleReceiver)
	}()
	wg.Wait()
	if senderErr != nil {
		t.Fatalf("sender dial: %v", senderErr)
	}
	if receiverErr != nil {
		t.Fatalf("receiver dial: %v", receiverErr)
	}
	defer senderConn.Close()
	defer receiverConn.Close()

	msg := []byte("hello through the relay")
	go senderConn.Write(msg)
	buf := make([]byte, len(msg))
	receiverConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(receiverConn, buf); err != nil {
		t.Fatalf("receiver read: %v", err)
	}
	if !bytes.Equal(buf, msg) {
		t.Errorf("got %q, want %q", buf, msg)
	}

	reply := []byte("and back again")
	go receiverConn.Write(reply)
	buf2 := make([]byte, len(reply))
	senderConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(senderConn, buf2); err != nil {
		t.Fatalf("sender read: %v", err)
	}
	if !bytes.Equal(buf2, reply) {
		t.Errorf("got %q, want %q", buf2, reply)
	}
}

func TestRelayRejectsDuplicateRoleForSameCode(t *testing.T) {
	addr, _ := startTestRelay(t)

	// The first sender registers and then legitimately blocks waiting for
	// a receiver that never shows up — it only returns once its own
	// context expires, so it must run in the background rather than be
	// awaited directly.
	firstCtx, firstCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer firstCancel()
	firstDone := make(chan struct{})
	go func() {
		DialRelay(firstCtx, addr, "dup-code", RoleSender)
		close(firstDone)
	}()

	// Give the first dial time to reach the server and register as
	// waiting before the second one races it (loopback, so generous
	// margin costs little).
	time.Sleep(300 * time.Millisecond)

	// A second sender for the same code must be rejected, not silently
	// queued or allowed to replace the first.
	shortCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := DialRelay(shortCtx, addr, "dup-code", RoleSender)
	if err != ErrRelayRejected {
		t.Errorf("expected ErrRelayRejected, got %v", err)
	}
	<-firstDone
}

func TestRelayDoesNotCrossPairDifferentCodes(t *testing.T) {
	addr, _ := startTestRelay(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 2)
	go func() {
		_, err := DialRelay(ctx, addr, "code-a", RoleSender)
		errCh <- err
	}()
	go func() {
		_, err := DialRelay(ctx, addr, "code-b", RoleReceiver)
		errCh <- err
	}()

	for range 2 {
		err := <-errCh
		if err == nil {
			t.Fatal("expected both dials to time out unmatched, got a successful pairing across different codes")
		}
	}
}
