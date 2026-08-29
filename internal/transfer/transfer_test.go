package transfer

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// cutConn forwards to an underlying net.Conn but forcibly severs the
// connection after a fixed number of bytes have been read, deterministically
// simulating a mid-transfer drop instead of relying on timing.
type cutConn struct {
	net.Conn
	limit int
	read  int
	cut   bool
}

func (c *cutConn) Read(p []byte) (int, error) {
	if c.cut {
		return 0, io.ErrClosedPipe
	}
	n, err := c.Conn.Read(p)
	c.read += n
	if c.read >= c.limit {
		c.cut = true
		c.Conn.Close()
	}
	return n, err
}

// connectedPair returns two ends of a real TCP loopback connection, which
// exercises the actual net.Conn read/write/deadline paths rather than an
// in-memory pipe.
func connectedPair(t *testing.T) (client, server net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	serverCh := make(chan net.Conn, 1)
	errCh := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			errCh <- err
			return
		}
		serverCh <- c
	}()

	client, err = net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	select {
	case server = <-serverCh:
	case err := <-errCh:
		t.Fatalf("accept: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for accept")
	}
	return client, server
}

func writeRandomFile(t *testing.T, dir, name string, size int) string {
	t.Helper()
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		t.Fatalf("rand: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	return path
}

func TestSendReceiveRoundTripSmallFile(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	srcPath := writeRandomFile(t, srcDir, "photo.jpg", 12345)

	client, server := connectedPair(t)
	defer client.Close()
	defer server.Close()

	const code = "crimson-otter-lagoon"
	errCh := make(chan error, 1)
	go func() { errCh <- Send(client, code, srcPath, SendOptions{}) }()

	if err := Receive(server, code, dstDir); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("send: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dstDir, "photo.jpg"))
	if err != nil {
		t.Fatalf("read received file: %v", err)
	}
	want, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read source file: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Error("received file does not match source byte-for-byte")
	}

	if _, err := os.Stat(filepath.Join(dstDir, "photo.jpg.part")); !os.IsNotExist(err) {
		t.Error("expected .part file to be gone after successful completion")
	}
	if _, err := os.Stat(filepath.Join(dstDir, "photo.jpg.part.meta")); !os.IsNotExist(err) {
		t.Error("expected .part.meta file to be gone after successful completion")
	}
}

func TestSendReceiveRoundTripMultiChunkFile(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	// A little over 3 chunks so we exercise the loop and the short final chunk.
	srcPath := writeRandomFile(t, srcDir, "big.bin", ChunkSize*3+777)

	client, server := connectedPair(t)
	defer client.Close()
	defer server.Close()

	const code = "spruce-comet-harbor"
	errCh := make(chan error, 1)
	go func() { errCh <- Send(client, code, srcPath, SendOptions{}) }()

	if err := Receive(server, code, dstDir); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("send: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dstDir, "big.bin"))
	if err != nil {
		t.Fatalf("read received file: %v", err)
	}
	want, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read source file: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Error("received multi-chunk file does not match source byte-for-byte")
	}
}

func TestSendReceiveEmptyFile(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	srcPath := writeRandomFile(t, srcDir, "empty.txt", 0)

	client, server := connectedPair(t)
	defer client.Close()
	defer server.Close()

	const code = "willow-ember-tundra"
	errCh := make(chan error, 1)
	go func() { errCh <- Send(client, code, srcPath, SendOptions{}) }()

	if err := Receive(server, code, dstDir); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("send: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dstDir, "empty.txt"))
	if err != nil {
		t.Fatalf("read received file: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty file, got %d bytes", len(got))
	}
}

func TestSendReceiveWrongCodeFailsClosed(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	srcPath := writeRandomFile(t, srcDir, "secret.txt", 100)

	client, server := connectedPair(t)
	defer client.Close()
	defer server.Close()

	errCh := make(chan error, 1)
	go func() { errCh <- Send(client, "correct-horse-battery", srcPath, SendOptions{}) }()

	err := Receive(server, "wrong-guess-entirely", dstDir)
	if err == nil {
		t.Fatal("expected receive to fail with mismatched code")
	}
	<-errCh // drain sender goroutine

	if _, statErr := os.Stat(filepath.Join(dstDir, "secret.txt")); !os.IsNotExist(statErr) {
		t.Error("no file should have been written when the handshake fails")
	}
}

func TestReceiveResumesAfterDroppedConnection(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	srcPath := writeRandomFile(t, srcDir, "resume.bin", ChunkSize*4)

	const code = "granite-falcon-meadow"

	// First attempt: deterministically sever the receiver's connection
	// partway through (after the handshake/header/resume-point exchange
	// but before the whole file has arrived) instead of relying on timing.
	client1, server1 := connectedPair(t)
	defer client1.Close()
	cut := &cutConn{Conn: server1, limit: 2 * ChunkSize}
	recvDone := make(chan error, 1)
	go func() { recvDone <- Receive(cut, code, dstDir) }()

	sendDone := make(chan error, 1)
	go func() { sendDone <- Send(client1, code, srcPath, SendOptions{}) }()

	if err := <-recvDone; !errors.Is(err, ErrConnectionLost) {
		t.Fatalf("expected first receive to report ErrConnectionLost, got %v", err)
	}
	<-sendDone // sender side error is expected too; not asserted on

	partPath := filepath.Join(dstDir, "resume.bin.part")
	if _, err := os.Stat(partPath); err != nil {
		t.Fatalf("expected partial file to survive the drop: %v", err)
	}
	partialBefore, err := os.ReadFile(partPath)
	if err != nil {
		t.Fatalf("read partial file: %v", err)
	}
	if len(partialBefore) == 0 {
		t.Fatal("expected some bytes to have already landed before the drop")
	}
	if len(partialBefore) >= ChunkSize*4 {
		t.Fatal("test didn't actually simulate a mid-transfer drop (partial file is already complete)")
	}

	// Second attempt, same code, fresh connection: must resume, not restart.
	client2, server2 := connectedPair(t)
	defer client2.Close()
	defer server2.Close()

	errCh := make(chan error, 1)
	go func() { errCh <- Send(client2, code, srcPath, SendOptions{}) }()
	if err := Receive(server2, code, dstDir); err != nil {
		t.Fatalf("resumed receive: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("resumed send: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dstDir, "resume.bin"))
	if err != nil {
		t.Fatalf("read final resumed file: %v", err)
	}
	want, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read source file: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Error("resumed file does not match source byte-for-byte")
	}
}
