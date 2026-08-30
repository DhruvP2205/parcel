package transfer

import "time"

// The three timeout tiers referenced throughout the design: how long a
// generated code waits to be claimed, how long a handshake gets to
// complete once two peers are connected, and how long the connection may
// go silent mid-transfer before it's treated as dropped.
const (
	CodeClaimTimeout = 2 * time.Minute
	HandshakeTimeout = 20 * time.Second
	StallTimeout     = 45 * time.Second

	// SessionWindow is how long a single generated code remains usable
	// end-to-end, including reconnect-and-resume attempts after a drop.
	// This folds the "separate resume token" idea from the design notes
	// into something simpler: the code itself stays valid for a bounded
	// window rather than being strictly single-shot, which is enough to
	// support resuming a dropped transfer without adding a second secret
	// to manage. See STDLIB.md / README for the reasoning.
	SessionWindow = 5 * time.Minute

	// LANDiscoveryTimeout bounds how long the CLI waits for a same-network
	// peer before falling back to a configured relay server. Direct LAN
	// transfer is always preferred (lower latency, no third-party server
	// in the path), but a real user shouldn't have to sit through the
	// full SessionWindow before an internet-relay fallback kicks in.
	LANDiscoveryTimeout = 15 * time.Second

	// PunchTimeout bounds the extra time spent trying to upgrade an
	// already-working relay connection to a direct NAT-punched one. This
	// is pure upside-seeking: the relay connection is already in hand by
	// the time punching starts, so a short budget here just avoids
	// stalling a transfer that would otherwise start immediately.
	PunchTimeout = 4 * time.Second
)
