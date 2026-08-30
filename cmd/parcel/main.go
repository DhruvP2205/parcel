// Command parcel sends and receives files or folders directly between two
// machines over an encrypted, resumable connection. No accounts, no cloud
// storage, no third-party packages.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"time"

	"parcel/internal/codeword"
	"parcel/internal/discovery"
	"parcel/internal/qr"
	"parcel/internal/source"
	"parcel/internal/transfer"
)

const usage = `parcel — zero-dependency encrypted P2P file transfer

Usage:
  parcel send <path> [flags]
  parcel receive <code> [flags]
  parcel relay [flags]

path may be a single file or a directory (sent as an archive and
unpacked back into a directory on the receiving end).

parcel always tries a direct local-network connection first. If that
doesn't find a peer within a few seconds and a relay server is configured
(-relay, or the PARCEL_RELAY environment variable), it falls back to that
relay — useful when the two machines aren't on the same network. Once
paired through the relay, both sides also try a brief direct-connection
upgrade (NAT hole punching) before settling for relayed traffic; this is
best-effort and depends on the network's NAT behavior, not guaranteed. The
relay never sees file content either way: every byte it forwards is
already end-to-end encrypted before it arrives.

Send/receive flags:
  -lan-only       only attempt local-network discovery, never contact a relay
  -relay-only     skip the local-network attempt, always use the relay
  -relay <addr>   relay server address, e.g. relay.example.com:4321
                  (also read from PARCEL_RELAY)
  -iface <name>   network interface name or IP to use for LAN discovery
                  (also read from PARCEL_LAN_IFACE) — set this on
                  multi-adapter machines (e.g. a VM host, or a laptop with
                  Wi-Fi + Ethernet + VPN) if discovery isn't finding your
                  peer; both sides must pick interfaces on the same network

Send-only flags:
  -no-compress    disable flate compression of the transferred stream
  -qr             also print the pairing code as a terminal QR code

Receive-only flags:
  -out <dir>      directory to write the received file/folder into (default ".")

Relay flags:
  -addr <addr>    address to listen on (default ":4321")

Examples:
  parcel send ./photo.jpg
  parcel send ./my-project-folder
  parcel receive crimson-otter-lagoon
  parcel send ./photo.jpg -relay relay.example.com:4321
  parcel relay -addr :4321
`

// exit codes
const (
	exitOK      = 0
	exitUsage   = 1
	exitRuntime = 2
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, usage)
		return exitUsage
	}

	switch args[0] {
	case "send":
		return runSend(args[1:])
	case "receive":
		return runReceive(args[1:])
	case "relay":
		return runRelay(args[1:])
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usage)
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "parcel: unknown command %q\n\n", args[0])
		fmt.Fprint(os.Stderr, usage)
		return exitUsage
	}
}

func runSend(args []string) int {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	lanOnly := fs.Bool("lan-only", false, "only attempt local-network discovery")
	relayOnly := fs.Bool("relay-only", false, "skip the local-network attempt, always use the relay")
	relayAddr := fs.String("relay", os.Getenv("PARCEL_RELAY"), "relay server address to fall back to (also read from PARCEL_RELAY)")
	ifaceOverride := fs.String("iface", os.Getenv("PARCEL_LAN_IFACE"), "network interface name or IP for LAN discovery (also read from PARCEL_LAN_IFACE)")
	noCompress := fs.Bool("no-compress", false, "disable compression")
	showQR := fs.Bool("qr", false, "also print the pairing code as a QR code")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "parcel send: expected exactly one <path> argument")
		return exitUsage
	}
	path := fs.Arg(0)

	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(os.Stderr, "parcel send: %v\n", err)
		return exitRuntime
	}
	if *lanOnly && *relayOnly {
		fmt.Fprintln(os.Stderr, "parcel send: -lan-only and -relay-only are mutually exclusive")
		return exitUsage
	}
	if *relayOnly && *relayAddr == "" {
		fmt.Fprintln(os.Stderr, "parcel send: -relay-only requires -relay <addr> (or PARCEL_RELAY)")
		return exitUsage
	}

	prepared, err := source.Prepare(path, !*noCompress)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parcel send: %v\n", err)
		return exitRuntime
	}
	defer prepared.Cleanup()
	if prepared.IsCompressed {
		fmt.Println("Compression helped — sending a compressed copy.")
	} else if !*noCompress {
		fmt.Println("Compression didn't help for this content — sending uncompressed.")
	}

	code, err := codeword.Generate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "parcel send: %v\n", err)
		return exitRuntime
	}

	ctx, cancel := context.WithTimeout(context.Background(), transfer.SessionWindow)
	defer cancel()

	var ln net.Listener
	if !*relayOnly {
		ln, err = net.Listen("tcp", ":0")
		if err != nil {
			fmt.Fprintf(os.Stderr, "parcel send: listen: %v\n", err)
			return exitRuntime
		}
		defer ln.Close()
		tcpPort := ln.Addr().(*net.TCPAddr).Port

		go func() {
			if err := discovery.Announce(ctx, code, tcpPort, *ifaceOverride); err != nil && ctx.Err() == nil {
				fmt.Fprintf(os.Stderr, "parcel send: LAN announce stopped early: %v\n", err)
			}
		}()
		go func() {
			<-ctx.Done()
			ln.Close()
		}()
	}

	fmt.Printf("Your code: %s\n", code)
	if *showQR {
		printQR(code)
	}
	fmt.Printf("Share it with the receiver — valid for %s. Waiting for them to connect...\n", transfer.SessionWindow)

	for {
		conn, err := acceptSendConn(ctx, ln, *relayOnly, *lanOnly, *relayAddr, code)
		if err != nil {
			if ctx.Err() != nil {
				fmt.Fprintln(os.Stderr, "parcel send: timed out waiting for a receiver")
				return exitRuntime
			}
			fmt.Fprintf(os.Stderr, "parcel send: %v\n", err)
			return exitRuntime
		}

		err = transfer.Send(conn, code, prepared.Path, transfer.SendOptions{
			Name:         prepared.Name,
			IsArchive:    prepared.IsArchive,
			IsCompressed: prepared.IsCompressed,
		})
		conn.Close()

		if err == nil {
			fmt.Println("Transfer complete.")
			return exitOK
		}
		if errors.Is(err, transfer.ErrConnectionLost) {
			fmt.Fprintf(os.Stderr, "parcel send: connection dropped (%v) — waiting for the receiver to reconnect...\n", err)
			continue
		}
		fmt.Fprintf(os.Stderr, "parcel send: %v\n", err)
		return exitRuntime
	}
}

func runReceive(args []string) int {
	fs := flag.NewFlagSet("receive", flag.ContinueOnError)
	out := fs.String("out", ".", "directory to write the received file/folder into")
	lanOnly := fs.Bool("lan-only", false, "only attempt local-network discovery")
	relayOnly := fs.Bool("relay-only", false, "skip the local-network attempt, always use the relay")
	relayAddr := fs.String("relay", os.Getenv("PARCEL_RELAY"), "relay server address to fall back to (also read from PARCEL_RELAY)")
	ifaceOverride := fs.String("iface", os.Getenv("PARCEL_LAN_IFACE"), "network interface name or IP for LAN discovery (also read from PARCEL_LAN_IFACE)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "parcel receive: expected exactly one <code> argument")
		return exitUsage
	}
	// QR's Alphanumeric mode (internal/qr) can only encode uppercase
	// letters, so a scanned code comes back shouted (ISO/IEC 18004's
	// Alphanumeric charset has no lowercase). Normalize here so a scanned
	// code and a typed one are byte-identical from this point on —
	// otherwise Validate, LAN discovery, and the handshake all fail on a
	// scan that's actually correct.
	code := strings.ToLower(fs.Arg(0))

	if err := codeword.Validate(code); err != nil {
		fmt.Fprintf(os.Stderr, "parcel receive: %q doesn't look like a valid code: %v\n", code, err)
		return exitUsage
	}
	if *lanOnly && *relayOnly {
		fmt.Fprintln(os.Stderr, "parcel receive: -lan-only and -relay-only are mutually exclusive")
		return exitUsage
	}
	if *relayOnly && *relayAddr == "" {
		fmt.Fprintln(os.Stderr, "parcel receive: -relay-only requires -relay <addr> (or PARCEL_RELAY)")
		return exitUsage
	}

	deadline := time.Now().Add(transfer.SessionWindow)
	backoff := time.Second
	const maxBackoff = 10 * time.Second

	for attempt := 1; ; attempt++ {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			fmt.Fprintln(os.Stderr, "parcel receive: timed out looking for a sender with that code")
			return exitRuntime
		}

		connectCtx, cancel := context.WithTimeout(context.Background(), remaining)
		conn, err := connectReceive(connectCtx, code, *lanOnly, *relayOnly, *relayAddr, *ifaceOverride)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "parcel receive: %v\n", err)
			return exitRuntime
		}

		if attempt == 1 {
			fmt.Printf("Connected to %v, receiving...\n", conn.RemoteAddr())
		} else {
			fmt.Printf("Reconnected to %v, resuming...\n", conn.RemoteAddr())
		}

		err = transfer.Receive(conn, code, *out)
		conn.Close()

		if err == nil {
			fmt.Println("Transfer complete.")
			return exitOK
		}
		if errors.Is(err, transfer.ErrConnectionLost) {
			fmt.Fprintf(os.Stderr, "parcel receive: connection dropped (%v) — retrying...\n", err)
			time.Sleep(backoff)
			if backoff < maxBackoff {
				backoff *= 2
			}
			continue
		}
		fmt.Fprintf(os.Stderr, "parcel receive: %v\n", err)
		return exitRuntime
	}
}

// printQR renders code as a terminal QR code (see internal/qr). This is a
// convenience layered on top of the spoken/typed code, which stays the
// primary, fully-verified pairing path — printQR degrades to a plain
// warning rather than failing the send if encoding ever errors (it never
// should for a code this package generates, but a QR failure is not a
// reason to abort a transfer).
func printQR(code string) {
	m, err := qr.Encode(strings.ToUpper(code))
	if err != nil {
		fmt.Fprintf(os.Stderr, "parcel send: could not render QR code: %v\n", err)
		return
	}
	fmt.Println("Or scan (unverified against a real camera in development — falls back to the code above if it doesn't scan):")
	fmt.Print(qr.Render(m))
}

func runRelay(args []string) int {
	fs := flag.NewFlagSet("relay", flag.ContinueOnError)
	addr := fs.String("addr", ":4321", "address to listen on for relay connections")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "parcel relay: unexpected arguments")
		return exitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fmt.Printf("Relay listening on %s — forwards encrypted bytes only, never sees file content. Ctrl+C to stop.\n", *addr)
	if err := discovery.RunRelay(ctx, *addr); err != nil {
		fmt.Fprintf(os.Stderr, "parcel relay: %v\n", err)
		return exitRuntime
	}
	return exitOK
}

// acceptSendConn waits for one incoming connection: on the local network
// (via ln, which Announce is advertising on), or through a relay, or both
// racing — whichever produces a peer first wins. See connectReceive for
// the receiving side of the same strategy.
func acceptSendConn(ctx context.Context, ln net.Listener, relayOnly, lanOnly bool, relayAddr, code string) (net.Conn, error) {
	if relayOnly {
		return connectViaRelayOrPunch(ctx, relayAddr, code, discovery.RoleSender)
	}

	lanCh, lanErrCh := make(chan net.Conn, 1), make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			lanErrCh <- err
			return
		}
		lanCh <- conn
	}()

	if lanOnly || relayAddr == "" {
		select {
		case conn := <-lanCh:
			return conn, nil
		case err := <-lanErrCh:
			return nil, err
		}
	}

	select {
	case conn := <-lanCh:
		return conn, nil
	case <-lanErrCh: // listener closed (likely ctx expiring) — let the relay race below decide
	case <-time.After(transfer.LANDiscoveryTimeout):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	relayCh, relayErrCh := make(chan net.Conn, 1), make(chan error, 1)
	go func() {
		conn, err := connectViaRelayOrPunch(ctx, relayAddr, code, discovery.RoleSender)
		if err != nil {
			relayErrCh <- err
			return
		}
		relayCh <- conn
	}()

	select {
	case conn := <-lanCh:
		go closeWhenReady(relayCh)
		return conn, nil
	case conn := <-relayCh:
		go closeWhenReady(lanCh)
		return conn, nil
	case relayErr := <-relayErrCh:
		select {
		case conn := <-lanCh:
			return conn, nil
		case lanErr := <-lanErrCh:
			return nil, fmt.Errorf("both local-network and relay attempts failed: lan=%v relay=%v", lanErr, relayErr)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// connectReceive is the receiving side of acceptSendConn's race: it tries
// LAN discovery first, then falls back to a relay if configured and LAN
// hasn't produced a peer within transfer.LANDiscoveryTimeout.
func connectReceive(ctx context.Context, code string, lanOnly, relayOnly bool, relayAddr, ifaceOverride string) (net.Conn, error) {
	if relayOnly {
		return connectViaRelayOrPunch(ctx, relayAddr, code, discovery.RoleReceiver)
	}

	lanCh, lanErrCh := make(chan net.Conn, 1), make(chan error, 1)
	go func() {
		conn, err := dialLAN(ctx, code, ifaceOverride)
		if err != nil {
			lanErrCh <- err
			return
		}
		lanCh <- conn
	}()

	if lanOnly || relayAddr == "" {
		select {
		case conn := <-lanCh:
			return conn, nil
		case err := <-lanErrCh:
			return nil, err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	select {
	case conn := <-lanCh:
		return conn, nil
	case <-lanErrCh:
	case <-time.After(transfer.LANDiscoveryTimeout):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	relayCh, relayErrCh := make(chan net.Conn, 1), make(chan error, 1)
	go func() {
		conn, err := connectViaRelayOrPunch(ctx, relayAddr, code, discovery.RoleReceiver)
		if err != nil {
			relayErrCh <- err
			return
		}
		relayCh <- conn
	}()

	select {
	case conn := <-lanCh:
		go closeWhenReady(relayCh)
		return conn, nil
	case conn := <-relayCh:
		go closeWhenReady(lanCh)
		return conn, nil
	case relayErr := <-relayErrCh:
		select {
		case conn := <-lanCh:
			return conn, nil
		case lanErr := <-lanErrCh:
			return nil, fmt.Errorf("both local-network and relay attempts failed: lan=%v relay=%v", lanErr, relayErr)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// connectViaRelayOrPunch pairs through the relay (the guaranteed path,
// exactly like M4) and then spends a short bounded window trying to
// upgrade to a direct NAT-punched connection using the peer address the
// relay's pairing handshake revealed. If punching succeeds, the relay
// connection — no longer needed — is closed and the direct one is used
// instead; if it fails or times out, the already-open relay connection is
// used as-is. Either way the caller gets exactly one connection back.
func connectViaRelayOrPunch(ctx context.Context, relayAddr, code string, role discovery.RelayRole) (net.Conn, error) {
	punchLn, lnErr := net.Listen("tcp", ":0")
	punchPort := 0
	if lnErr == nil {
		punchPort = punchLn.Addr().(*net.TCPAddr).Port
	}

	relayConn, peer, err := discovery.DialRelay(ctx, relayAddr, code, role, punchPort)
	if err != nil {
		if punchLn != nil {
			punchLn.Close()
		}
		return nil, err
	}
	if punchLn == nil || peer.PunchPort == 0 {
		return relayConn, nil
	}

	punchCtx, cancel := context.WithTimeout(ctx, transfer.PunchTimeout)
	direct, punchErr := discovery.PunchDirect(punchCtx, role, punchLn, peer)
	cancel()
	punchLn.Close()
	if punchErr != nil {
		return relayConn, nil
	}
	relayConn.Close()
	return direct, nil
}

func dialLAN(ctx context.Context, code, ifaceOverride string) (net.Conn, error) {
	ip, port, err := discovery.Discover(ctx, code, ifaceOverride)
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp", (&net.TCPAddr{IP: ip, Port: port}).String())
}

// closeWhenReady closes whichever connection eventually arrives on ch —
// used to clean up the losing side of an accept/dial race so it doesn't
// sit paired-but-unused. Gives up after a bounded wait rather than
// blocking forever if the loser never arrives.
func closeWhenReady(ch <-chan net.Conn) {
	select {
	case conn := <-ch:
		conn.Close()
	case <-time.After(30 * time.Second):
	}
}
