// Package discovery finds a sender and receiver on the same local network
// (no rendezvous server needed) and, in later milestones, falls back to an
// internet rendezvous when they aren't on the same network.
//
// LAN discovery works over UDP multicast: the sender periodically
// broadcasts a small beacon tagged with a value derived from the pairing
// code, and the receiver, who also knows the code, listens for a beacon
// whose tag matches and connects directly over TCP to the address it came
// from.
//
// The beacon tag deliberately does NOT use a fast hash (HMAC/SHA-256)
// alone. Unlike the session handshake in internal/crypto — which requires
// an attacker to be actively on-path to test a guess — the beacon is
// broadcast in the clear to the whole local network. Anyone on the LAN can
// record one beacon and, offline, try candidate codes against it with no
// rate limit. Stretching the tag with PBKDF2 (crypto/pbkdf2, RFC 8018)
// raises the cost of each offline guess by the iteration count, turning a
// few-minutes brute force into a meaningfully slower one. This is a
// deliberate, documented tradeoff, not a substitute for keeping codes
// short-lived.
package discovery

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"
)

const (
	multicastGroup   = "239.255.42.99"
	multicastPort    = 9034
	beaconMagic      = "PRCL1"
	beaconInterval   = 1 * time.Second
	pbkdf2Iterations = 200_000
	tagLength        = 16
	saltLength       = 16
	// magic(5) + salt(16) + tag(16) + port(2)
	beaconLength = len(beaconMagic) + saltLength + tagLength + 2
)

// ErrNoPeerFound means Discover's context expired before a matching beacon
// arrived — nobody on this network is currently sending with this code.
var ErrNoPeerFound = errors.New("discovery: no matching peer found on the local network before timeout")

func groupAddr() (*net.UDPAddr, error) {
	return net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", multicastGroup, multicastPort))
}

// multicastInterface picks a concrete, up, multicast-capable, non-loopback
// interface with an IPv4 address. Both Announce and Discover are pinned to
// the same explicit interface rather than letting the OS pick one for
// each independently — on multi-interface machines (Wi-Fi + Ethernet +
// virtual adapters, which is the common case on a laptop) the two can
// disagree, and the beacon silently never arrives.
func multicastInterface() (*net.Interface, net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, nil, fmt.Errorf("discovery: list interfaces: %w", err)
	}
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil {
				continue
			}
			ifiCopy := ifi
			return &ifiCopy, ip4, nil
		}
	}
	return nil, nil, errors.New("discovery: no up, multicast-capable IPv4 network interface found")
}

func beaconTag(code string, salt []byte) ([]byte, error) {
	return pbkdf2.Key(sha256.New, code, salt, pbkdf2Iterations, tagLength)
}

// Announce periodically broadcasts a beacon for code advertising tcpPort
// until ctx is cancelled. Intended to run in its own goroutine on the
// sending side, in parallel with net.Listener.Accept.
func Announce(ctx context.Context, code string, tcpPort int) error {
	addr, err := groupAddr()
	if err != nil {
		return fmt.Errorf("discovery: resolve multicast group: %w", err)
	}
	_, ifaceIP, err := multicastInterface()
	if err != nil {
		return err
	}
	conn, err := net.DialUDP("udp4", &net.UDPAddr{IP: ifaceIP}, addr)
	if err != nil {
		return fmt.Errorf("discovery: dial multicast group: %w", err)
	}
	defer conn.Close()

	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("discovery: generate salt: %w", err)
	}
	tag, err := beaconTag(code, salt)
	if err != nil {
		return fmt.Errorf("discovery: derive beacon tag: %w", err)
	}

	packet := make([]byte, beaconLength)
	copy(packet, beaconMagic)
	copy(packet[len(beaconMagic):], salt)
	copy(packet[len(beaconMagic)+saltLength:], tag)
	binary.BigEndian.PutUint16(packet[len(beaconMagic)+saltLength+tagLength:], uint16(tcpPort))

	ticker := time.NewTicker(beaconInterval)
	defer ticker.Stop()

	for {
		if _, err := conn.Write(packet); err != nil {
			return fmt.Errorf("discovery: send beacon: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Discover listens for a beacon matching code and returns the sender's
// address and TCP port to connect to. It blocks until a match arrives or
// ctx is done.
func Discover(ctx context.Context, code string) (net.IP, int, error) {
	addr, err := groupAddr()
	if err != nil {
		return nil, 0, fmt.Errorf("discovery: resolve multicast group: %w", err)
	}
	ifi, _, err := multicastInterface()
	if err != nil {
		return nil, 0, err
	}
	conn, err := net.ListenMulticastUDP("udp4", ifi, addr)
	if err != nil {
		return nil, 0, fmt.Errorf("discovery: join multicast group: %w", err)
	}
	defer conn.Close()

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	buf := make([]byte, 512)
	for {
		n, srcAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil, 0, ErrNoPeerFound
			}
			return nil, 0, fmt.Errorf("discovery: read beacon: %w", err)
		}
		if n != beaconLength {
			continue // not our beacon shape, ignore
		}
		packet := buf[:n]
		if string(packet[:len(beaconMagic)]) != beaconMagic {
			continue
		}
		salt := packet[len(beaconMagic) : len(beaconMagic)+saltLength]
		observedTag := packet[len(beaconMagic)+saltLength : len(beaconMagic)+saltLength+tagLength]
		portBytes := packet[len(beaconMagic)+saltLength+tagLength:]

		expectedTag, err := beaconTag(code, salt)
		if err != nil {
			return nil, 0, fmt.Errorf("discovery: derive beacon tag: %w", err)
		}
		if subtle.ConstantTimeCompare(expectedTag, observedTag) != 1 {
			continue // someone else's beacon (different code)
		}

		port := int(binary.BigEndian.Uint16(portBytes))
		return srcAddr.IP, port, nil
	}
}
