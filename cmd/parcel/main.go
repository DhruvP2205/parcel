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
	"time"

	"parcel/internal/codeword"
	"parcel/internal/discovery"
	"parcel/internal/source"
	"parcel/internal/transfer"
)

const usage = `parcel — zero-dependency encrypted P2P file transfer

Usage:
  parcel send <path> [flags]
  parcel receive <code> [flags]

path may be a single file or a directory (sent as an archive and
unpacked back into a directory on the receiving end).

Send flags:
  -lan-only       only attempt local-network discovery, never contact a relay
  -relay-only     skip direct/LAN attempts, always use the relay (not yet implemented)
  -no-compress    disable flate compression of the transferred stream

Receive flags:
  -out <dir>      directory to write the received file/folder into (default ".")

Examples:
  parcel send ./photo.jpg
  parcel send ./my-project-folder
  parcel receive crimson-otter-lagoon
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
	relayOnly := fs.Bool("relay-only", false, "always use the relay, skip direct/LAN attempts")
	noCompress := fs.Bool("no-compress", false, "disable compression")
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
	if *relayOnly {
		fmt.Fprintln(os.Stderr, "parcel send: -relay-only is not implemented yet (relay/rendezvous ships in a later milestone)")
		return exitRuntime
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

	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "parcel send: listen: %v\n", err)
		return exitRuntime
	}
	defer ln.Close()
	tcpPort := ln.Addr().(*net.TCPAddr).Port

	fmt.Printf("Your code: %s\n", code)
	fmt.Printf("Share it with the receiver — valid for %s. Waiting for them to connect...\n", transfer.SessionWindow)

	ctx, cancel := context.WithTimeout(context.Background(), transfer.SessionWindow)
	defer cancel()

	go func() {
		if err := discovery.Announce(ctx, code, tcpPort); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "parcel send: LAN announce stopped early: %v\n", err)
		}
	}()
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				fmt.Fprintln(os.Stderr, "parcel send: timed out waiting for a receiver")
				return exitRuntime
			}
			fmt.Fprintf(os.Stderr, "parcel send: accept: %v\n", err)
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
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "parcel receive: expected exactly one <code> argument")
		return exitUsage
	}
	code := fs.Arg(0)

	if err := codeword.Validate(code); err != nil {
		fmt.Fprintf(os.Stderr, "parcel receive: %q doesn't look like a valid code: %v\n", code, err)
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

		discoverCtx, cancel := context.WithTimeout(context.Background(), min(remaining, transfer.CodeClaimTimeout))
		ip, port, err := discovery.Discover(discoverCtx, code)
		cancel()
		if err != nil {
			if errors.Is(err, discovery.ErrNoPeerFound) {
				fmt.Fprintln(os.Stderr, "parcel receive: no sender found on the local network with that code")
				return exitRuntime
			}
			fmt.Fprintf(os.Stderr, "parcel receive: discovery: %v\n", err)
			return exitRuntime
		}

		addr := &net.TCPAddr{IP: ip, Port: port}
		conn, err := net.DialTCP("tcp", nil, addr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parcel receive: connect to %v: %v\n", addr, err)
			return exitRuntime
		}

		if attempt == 1 {
			fmt.Printf("Found sender at %v, connecting...\n", addr)
		} else {
			fmt.Printf("Reconnected to %v, resuming...\n", addr)
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
