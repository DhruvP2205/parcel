package transfer

import (
	"fmt"
	"net"

	pcrypto "parcel/internal/crypto"
)

// PerformHandshake runs the code-authenticated X25519 key exchange over
// conn and returns the derived session plus both sides' raw public keys
// (needed by callers only for logging/debugging; the confirmation check
// already happened here). A non-nil error means the handshake did not
// complete and no file bytes may be sent or trusted.
func PerformHandshake(conn net.Conn, code string, role pcrypto.Role) (session *pcrypto.Session, initiatorPub, responderPub []byte, err error) {
	kp, err := pcrypto.GenerateKeyPair()
	if err != nil {
		return nil, nil, nil, err
	}
	myPub := kp.PublicBytes()

	peerPubBytes, err := exchange(conn, myPub, maxHandshakeFrame)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("transfer: exchange public keys: %w", err)
	}
	peerPub, err := pcrypto.ParsePublicKey(peerPubBytes)
	if err != nil {
		return nil, nil, nil, err
	}

	if role == pcrypto.RoleInitiator {
		initiatorPub, responderPub = myPub, peerPubBytes
	} else {
		initiatorPub, responderPub = peerPubBytes, myPub
	}

	session, err = pcrypto.DeriveSession(kp, peerPub, code, role)
	if err != nil {
		return nil, nil, nil, err
	}

	myConfirm := session.Confirm(initiatorPub, responderPub)
	peerConfirm, err := exchange(conn, myConfirm, maxHandshakeFrame)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("transfer: exchange confirmation: %w", err)
	}
	if err := session.VerifyConfirm(initiatorPub, responderPub, peerConfirm); err != nil {
		return nil, nil, nil, err
	}

	return session, initiatorPub, responderPub, nil
}

// exchange writes mine as a frame and concurrently reads the peer's frame,
// avoiding a write/read deadlock when both sides run this same code.
func exchange(conn net.Conn, mine []byte, maxLen int) ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}
	readCh := make(chan result, 1)
	go func() {
		data, err := readFrame(conn, maxLen)
		readCh <- result{data, err}
	}()

	if err := writeFrame(conn, mine); err != nil {
		return nil, err
	}
	res := <-readCh
	return res.data, res.err
}
