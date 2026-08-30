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
	"regexp"
	"strings"
	"time"

	"parcel/internal/ansi"
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
  -relay <addrs>  relay server address, e.g. relay.example.com:4321 — or a
                  comma-separated list (relay1:4321,relay2:4321) tried in
                  order, so a second address is used if the first is
                  unreachable. Both sides must be given the same list in
                  the same order, since pairing only happens between
                  clients that land on the same relay process.
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
  parcel receive crimson-otter-lagoon-basil
  parcel send ./photo.jpg -relay relay.example.com:4321
  parcel relay -addr :4321
`

// exit codes
const (
	exitOK      = 0
	exitUsage   = 1
	exitRuntime = 2
)

// usage is colorized by pattern-matching over the plain-text template above
// rather than maintaining a second copy — headers, flag tokens, and the
// "parcel <subcommand>" pairs get highlighted, everything else prints as-is.
var (
	usageHeaderRe = regexp.MustCompile(`(?m)^(Usage:|Send/receive flags:|Send-only flags:|Receive-only flags:|Relay flags:|Examples:)$`)
	usageFlagRe   = regexp.MustCompile(`(?m)^(  -[\w-]+)`)
	usageCmdRe    = regexp.MustCompile(`\bparcel (send|receive|relay)\b`)
)

func colorUsage(enabled bool) string {
	if !enabled {
		return usage
	}
	s := usage
	s = strings.Replace(s, "parcel — zero-dependency", ansi.BoldMagenta+"parcel"+ansi.Reset+" — zero-dependency", 1)
	s = usageHeaderRe.ReplaceAllString(s, ansi.HeaderStyle+"$1"+ansi.Reset)
	s = usageFlagRe.ReplaceAllString(s, ansi.Yellow+"$1"+ansi.Reset)
	s = usageCmdRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := strings.TrimPrefix(m, "parcel ")
		return ansi.Bold + "parcel" + ansi.Reset + " " + ansi.Green + sub + ansi.Reset
	})
	return s
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, colorUsage(ansi.StderrEnabled))
		return exitUsage
	}

	switch args[0] {
	case "send":
		return runSend(args[1:])
	case "receive":
		return runReceive(args[1:])
	case "relay":
		return runRelay(args[1:])
	case "-h", "-help", "--help", "help":
		fmt.Fprint(os.Stdout, colorUsage(ansi.StdoutEnabled))
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "%s\n\n", ansi.Err(ansi.BoldRed, fmt.Sprintf("parcel: unknown command %q", args[0])))
		fmt.Fprint(os.Stderr, colorUsage(ansi.StderrEnabled))
		return exitUsage
	}
}

func runSend(args []string) int {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	lanOnly := fs.Bool("lan-only", false, "only attempt local-network discovery")
	relayOnly := fs.Bool("relay-only", false, "skip the local-network attempt, always use the relay")
	relayAddr := fs.String("relay", os.Getenv("PARCEL_RELAY"), "comma-separated relay server address(es) to fall back to, tried in order (also read from PARCEL_RELAY)")
	ifaceOverride := fs.String("iface", os.Getenv("PARCEL_LAN_IFACE"), "network interface name or IP for LAN discovery (also read from PARCEL_LAN_IFACE)")
	noCompress := fs.Bool("no-compress", false, "disable compression")
	showQR := fs.Bool("qr", false, "also print the pairing code as a QR code")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, ansi.Err(ansi.Red, "parcel send: expected exactly one <path> argument"))
		return exitUsage
	}
	path := fs.Arg(0)
	relayAddrs := parseRelayAddrs(*relayAddr)

	if _, err := os.Stat(path); err != nil {
		fmt.Fprintln(os.Stderr, ansi.Err(ansi.Red, fmt.Sprintf("parcel send: %v", err)))
		return exitRuntime
	}
	if *lanOnly && *relayOnly {
		fmt.Fprintln(os.Stderr, ansi.Err(ansi.Red, "parcel send: -lan-only and -relay-only are mutually exclusive"))
		return exitUsage
	}
	if *relayOnly && len(relayAddrs) == 0 {
		fmt.Fprintln(os.Stderr, ansi.Err(ansi.Red, "parcel send: -relay-only requires -relay <addr>[,<addr>...] (or PARCEL_RELAY)"))
		return exitUsage
	}

	prepared, err := source.Prepare(path, !*noCompress)
	if err != nil {
		fmt.Fprintln(os.Stderr, ansi.Err(ansi.Red, fmt.Sprintf("parcel send: %v", err)))
		return exitRuntime
	}
	defer prepared.Cleanup()
	if prepared.IsCompressed {
		fmt.Println(ansi.Out(ansi.Dim, "Compression helped — sending a compressed copy."))
	} else if !*noCompress {
		fmt.Println(ansi.Out(ansi.Dim, "Compression didn't help for this content — sending uncompressed."))
	}

	code, err := codeword.Generate()
	if err != nil {
		fmt.Fprintln(os.Stderr, ansi.Err(ansi.Red, fmt.Sprintf("parcel send: %v", err)))
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

	fmt.Printf("Your code: %s\n", ansi.Out(ansi.BoldMagenta, code))
	if *showQR {
		printQR(code)
	}
	fmt.Println(ansi.Out(ansi.Cyan, fmt.Sprintf("Share it with the receiver — valid for %s.", transfer.SessionWindow)))

	for {
		sp := ansi.StartSpinner("Waiting for them to connect...")
		conn, err := acceptSendConn(ctx, ln, *relayOnly, *lanOnly, relayAddrs, code)
		sp.Stop("")
		if err != nil {
			if ctx.Err() != nil {
				fmt.Fprintln(os.Stderr, ansi.Err(ansi.Red, "parcel send: timed out waiting for a receiver"))
				return exitRuntime
			}
			fmt.Fprintln(os.Stderr, ansi.Err(ansi.Red, fmt.Sprintf("parcel send: %v", err)))
			if errors.Is(err, discovery.ErrAllRelaysUnreachable) {
				adviseSelfHostRelay()
			}
			return exitRuntime
		}

		err = transfer.Send(conn, code, prepared.Path, transfer.SendOptions{
			Name:         prepared.Name,
			IsArchive:    prepared.IsArchive,
			IsCompressed: prepared.IsCompressed,
		})
		conn.Close()

		if err == nil {
			fmt.Println(ansi.Out(ansi.BoldGreen, "✔ Transfer complete."))
			return exitOK
		}
		if errors.Is(err, transfer.ErrConnectionLost) {
			fmt.Fprintln(os.Stderr, ansi.Err(ansi.Yellow, fmt.Sprintf("parcel send: connection dropped (%v) — waiting for the receiver to reconnect...", err)))
			continue
		}
		fmt.Fprintln(os.Stderr, ansi.Err(ansi.Red, fmt.Sprintf("parcel send: %v", err)))
		return exitRuntime
	}
}

func runReceive(args []string) int {
	fs := flag.NewFlagSet("receive", flag.ContinueOnError)
	out := fs.String("out", ".", "directory to write the received file/folder into")
	lanOnly := fs.Bool("lan-only", false, "only attempt local-network discovery")
	relayOnly := fs.Bool("relay-only", false, "skip the local-network attempt, always use the relay")
	relayAddr := fs.String("relay", os.Getenv("PARCEL_RELAY"), "comma-separated relay server address(es) to fall back to, tried in order (also read from PARCEL_RELAY)")
	ifaceOverride := fs.String("iface", os.Getenv("PARCEL_LAN_IFACE"), "network interface name or IP for LAN discovery (also read from PARCEL_LAN_IFACE)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, ansi.Err(ansi.Red, "parcel receive: expected exactly one <code> argument"))
		return exitUsage
	}
	relayAddrs := parseRelayAddrs(*relayAddr)
	// QR's Alphanumeric mode (internal/qr) can only encode uppercase
	// letters, so a scanned code comes back shouted (ISO/IEC 18004's
	// Alphanumeric charset has no lowercase). Normalize here so a scanned
	// code and a typed one are byte-identical from this point on —
	// otherwise Validate, LAN discovery, and the handshake all fail on a
	// scan that's actually correct.
	code := strings.ToLower(fs.Arg(0))

	if err := codeword.Validate(code); err != nil {
		fmt.Fprintln(os.Stderr, ansi.Err(ansi.Red, fmt.Sprintf("parcel receive: %q doesn't look like a valid code: %v", code, err)))
		return exitUsage
	}
	if *lanOnly && *relayOnly {
		fmt.Fprintln(os.Stderr, ansi.Err(ansi.Red, "parcel receive: -lan-only and -relay-only are mutually exclusive"))
		return exitUsage
	}
	if *relayOnly && len(relayAddrs) == 0 {
		fmt.Fprintln(os.Stderr, ansi.Err(ansi.Red, "parcel receive: -relay-only requires -relay <addr>[,<addr>...] (or PARCEL_RELAY)"))
		return exitUsage
	}

	deadline := time.Now().Add(transfer.SessionWindow)
	backoff := time.Second
	const maxBackoff = 10 * time.Second

	for attempt := 1; ; attempt++ {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			fmt.Fprintln(os.Stderr, ansi.Err(ansi.Red, "parcel receive: timed out looking for a sender with that code"))
			return exitRuntime
		}

		spinnerLabel := "Looking for a sender with that code..."
		if attempt > 1 {
			spinnerLabel = "Reconnecting..."
		}
		sp := ansi.StartSpinner(spinnerLabel)
		connectCtx, cancel := context.WithTimeout(context.Background(), remaining)
		conn, err := connectReceive(connectCtx, code, *lanOnly, *relayOnly, relayAddrs, *ifaceOverride)
		cancel()
		sp.Stop("")
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				fmt.Fprintln(os.Stderr, ansi.Err(ansi.Red, "parcel receive: timed out looking for a sender with that code"))
				return exitRuntime
			}
			fmt.Fprintln(os.Stderr, ansi.Err(ansi.Red, fmt.Sprintf("parcel receive: %v", err)))
			if errors.Is(err, discovery.ErrAllRelaysUnreachable) {
				adviseSelfHostRelay()
			}
			return exitRuntime
		}

		if attempt == 1 {
			fmt.Println(ansi.Out(ansi.Cyan, fmt.Sprintf("Connected to %v, receiving...", conn.RemoteAddr())))
		} else {
			fmt.Println(ansi.Out(ansi.Cyan, fmt.Sprintf("Reconnected to %v, resuming...", conn.RemoteAddr())))
		}

		err = transfer.Receive(conn, code, *out)
		conn.Close()

		if err == nil {
			fmt.Println(ansi.Out(ansi.BoldGreen, "✔ Transfer complete."))
			return exitOK
		}
		if errors.Is(err, transfer.ErrConnectionLost) {
			fmt.Fprintln(os.Stderr, ansi.Err(ansi.Yellow, fmt.Sprintf("parcel receive: connection dropped (%v) — retrying...", err)))
			time.Sleep(backoff)
			if backoff < maxBackoff {
				backoff *= 2
			}
			continue
		}
		fmt.Fprintln(os.Stderr, ansi.Err(ansi.Red, fmt.Sprintf("parcel receive: %v", err)))
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
		fmt.Fprintln(os.Stderr, ansi.Err(ansi.Yellow, fmt.Sprintf("parcel send: could not render QR code: %v", err)))
		return
	}
	fmt.Println(ansi.Out(ansi.Dim, "Or scan (unverified against a real camera in development — falls back to the code above if it doesn't scan):"))
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
		fmt.Fprintln(os.Stderr, ansi.Err(ansi.Red, "parcel relay: unexpected arguments"))
		return exitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fmt.Println(ansi.Out(ansi.BoldGreen, fmt.Sprintf("Relay listening on %s — forwards encrypted bytes only, never sees file content. Ctrl+C to stop.", *addr)))
	if err := discovery.RunRelay(ctx, *addr); err != nil {
		fmt.Fprintln(os.Stderr, ansi.Err(ansi.Red, fmt.Sprintf("parcel relay: %v", err)))
		return exitRuntime
	}
	return exitOK
}

// parseRelayAddrs splits a comma-separated -relay/PARCEL_RELAY value into
// its individual addresses, trimming whitespace and dropping empty entries
// (so "" and "a:1, ,b:2" both behave sensibly). No addresses are ever
// assumed by default — an empty result means "no relay configured," never
// a hidden fallback to some baked-in server.
func parseRelayAddrs(raw string) []string {
	var addrs []string
	for a := range strings.SplitSeq(raw, ",") {
		if a = strings.TrimSpace(a); a != "" {
			addrs = append(addrs, a)
		}
	}
	return addrs
}

// adviseSelfHostRelay prints actionable next steps when every configured
// relay address turned out to be unreachable, rather than leaving the user
// with just a bare dial-error string.
func adviseSelfHostRelay() {
	fmt.Fprintln(os.Stderr, ansi.Err(ansi.Yellow, "None of the configured relay addresses could be reached."))
	fmt.Fprintln(os.Stderr, ansi.Err(ansi.Yellow, "You can run your own on any machine reachable from both sides:"))
	fmt.Fprintln(os.Stderr, ansi.Err(ansi.Yellow, "  parcel relay -addr :4321"))
	fmt.Fprintln(os.Stderr, ansi.Err(ansi.Yellow, "then point both send and receive at it: -relay <that machine's address>:4321 (or set PARCEL_RELAY)"))
}

// acceptSendConn waits for one incoming connection: on the local network
// (via ln, which Announce is advertising on), or through a relay, or both
// racing — whichever produces a peer first wins. See connectReceive for
// the receiving side of the same strategy.
func acceptSendConn(ctx context.Context, ln net.Listener, relayOnly, lanOnly bool, relayAddrs []string, code string) (net.Conn, error) {
	if relayOnly {
		return connectViaRelayOrPunch(ctx, relayAddrs, code, discovery.RoleSender)
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

	if lanOnly || len(relayAddrs) == 0 {
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
		conn, err := connectViaRelayOrPunch(ctx, relayAddrs, code, discovery.RoleSender)
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
			return nil, fmt.Errorf("both local-network and relay attempts failed: lan=%w relay=%w", lanErr, relayErr)
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
func connectReceive(ctx context.Context, code string, lanOnly, relayOnly bool, relayAddrs []string, ifaceOverride string) (net.Conn, error) {
	if relayOnly {
		return connectViaRelayOrPunch(ctx, relayAddrs, code, discovery.RoleReceiver)
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

	if lanOnly || len(relayAddrs) == 0 {
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
		conn, err := connectViaRelayOrPunch(ctx, relayAddrs, code, discovery.RoleReceiver)
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
			return nil, fmt.Errorf("both local-network and relay attempts failed: lan=%w relay=%w", lanErr, relayErr)
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
func connectViaRelayOrPunch(ctx context.Context, relayAddrs []string, code string, role discovery.RelayRole) (net.Conn, error) {
	punchLn, lnErr := net.Listen("tcp", ":0")
	punchPort := 0
	if lnErr == nil {
		punchPort = punchLn.Addr().(*net.TCPAddr).Port
	}

	relayConn, peer, err := discovery.DialRelayFallback(ctx, relayAddrs, code, role, punchPort)
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
