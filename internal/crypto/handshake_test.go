package crypto

import "testing"

func TestHandshakeMatchingCodeDerivesConsistentKeys(t *testing.T) {
	initKP, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate initiator keypair: %v", err)
	}
	respKP, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate responder keypair: %v", err)
	}

	initPub, err := ParsePublicKey(respKP.PublicBytes())
	if err != nil {
		t.Fatalf("parse responder pub: %v", err)
	}
	respPub, err := ParsePublicKey(initKP.PublicBytes())
	if err != nil {
		t.Fatalf("parse initiator pub: %v", err)
	}

	const code = "7-crimson-anchor"
	initSession, err := DeriveSession(initKP, initPub, code, RoleInitiator)
	if err != nil {
		t.Fatalf("derive initiator session: %v", err)
	}
	respSession, err := DeriveSession(respKP, respPub, code, RoleResponder)
	if err != nil {
		t.Fatalf("derive responder session: %v", err)
	}

	if initSession.SendKey != respSession.RecvKey {
		t.Error("initiator SendKey must equal responder RecvKey")
	}
	if initSession.RecvKey != respSession.SendKey {
		t.Error("initiator RecvKey must equal responder SendKey")
	}

	initiatorPub := initKP.PublicBytes()
	responderPub := respKP.PublicBytes()

	mac := initSession.Confirm(initiatorPub, responderPub)
	if err := respSession.VerifyConfirm(initiatorPub, responderPub, mac); err != nil {
		t.Errorf("responder failed to verify initiator's confirmation: %v", err)
	}
}

func TestHandshakeMismatchedCodeFailsClosed(t *testing.T) {
	initKP, _ := GenerateKeyPair()
	respKP, _ := GenerateKeyPair()
	initPub, _ := ParsePublicKey(respKP.PublicBytes())
	respPub, _ := ParsePublicKey(initKP.PublicBytes())

	initSession, err := DeriveSession(initKP, initPub, "correct-horse-battery", RoleInitiator)
	if err != nil {
		t.Fatalf("derive initiator session: %v", err)
	}
	respSession, err := DeriveSession(respKP, respPub, "wrong-guess-entirely", RoleResponder)
	if err != nil {
		t.Fatalf("derive responder session: %v", err)
	}

	if initSession.SendKey == respSession.RecvKey {
		t.Fatal("sessions with different codes must not derive matching keys")
	}

	initiatorPub := initKP.PublicBytes()
	responderPub := respKP.PublicBytes()
	mac := initSession.Confirm(initiatorPub, responderPub)
	if err := respSession.VerifyConfirm(initiatorPub, responderPub, mac); err == nil {
		t.Error("expected confirmation to fail with mismatched codes, got nil error")
	}
}

func TestHandshakeTamperedTranscriptFailsClosed(t *testing.T) {
	initKP, _ := GenerateKeyPair()
	respKP, _ := GenerateKeyPair()
	initPub, _ := ParsePublicKey(respKP.PublicBytes())
	respPub, _ := ParsePublicKey(initKP.PublicBytes())

	const code = "shared-secret-phrase"
	initSession, _ := DeriveSession(initKP, initPub, code, RoleInitiator)
	respSession, _ := DeriveSession(respKP, respPub, code, RoleResponder)

	initiatorPub := initKP.PublicBytes()
	responderPub := respKP.PublicBytes()
	mac := initSession.Confirm(initiatorPub, responderPub)

	tamperedResponderPub := append([]byte{}, responderPub...)
	tamperedResponderPub[0] ^= 0xFF

	if err := respSession.VerifyConfirm(initiatorPub, tamperedResponderPub, mac); err == nil {
		t.Error("expected confirmation to fail when transcript is tampered with, got nil error")
	}
}

func TestGenerateKeyPairProducesDistinctKeys(t *testing.T) {
	a, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	b, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if string(a.PublicBytes()) == string(b.PublicBytes()) {
		t.Error("two independently generated keypairs must not collide")
	}
}
