package discovery

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"parcel/internal/transfer"
)

// Proves the full send/receive pipeline (handshake, encrypted chunks,
// completion confirmation) works unmodified over a relay-provided
// connection, not just direct TCP — the relay is transparent to
// internal/transfer by design.
func TestFileTransferThroughRelay(t *testing.T) {
	addr, _ := startTestRelay(t)

	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "photo.bin")
	content := bytes.Repeat([]byte("relay-e2e-payload-"), 20000) // multi-chunk
	if err := os.WriteFile(srcPath, content, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	dstDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var senderConn, receiverConn net.Conn
	var senderErr, receiverErr error
	done := make(chan struct{}, 2)
	go func() {
		senderConn, senderErr = DialRelay(ctx, addr, "relay-transfer-code", RoleSender)
		done <- struct{}{}
	}()
	go func() {
		receiverConn, receiverErr = DialRelay(ctx, addr, "relay-transfer-code", RoleReceiver)
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
	defer senderConn.Close()
	defer receiverConn.Close()

	const code = "amber-falcon-summit"
	errCh := make(chan error, 1)
	go func() {
		errCh <- transfer.Send(senderConn, code, srcPath, transfer.SendOptions{})
	}()
	if err := transfer.Receive(receiverConn, code, dstDir); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("send: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dstDir, "photo.bin"))
	if err != nil {
		t.Fatalf("read received file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Error("received content does not match source")
	}
}
