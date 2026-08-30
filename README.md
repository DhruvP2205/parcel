# parcel

A zero-dependency, peer-to-peer, end-to-end encrypted file/folder transfer
CLI. Two people share a short spoken code; parcel connects them directly
on a LAN when it can, falls back to relaying through a server (or a
best-effort direct NAT-punched connection) when it can't, and resumes a
dropped transfer instead of starting over. No accounts, no cloud storage,
no third-party packages — `go.mod` has no `require` block.

Built for the **Zero Dependency** hackathon, **Track C (Web & Network)**.

## What it actually does

- **Pairing**: `parcel send` prints a short code like `crimson-otter-
  lagoon` (three words, drawn with `crypto/rand`). `parcel receive
  <code>` on the other machine finds and connects to the sender using
  that code — nothing is typed except the code itself.
- **Encryption**: an X25519 key exchange (`crypto/ecdh`) authenticated by
  the shared code, then AES-256-GCM (`crypto/aes`+`crypto/cipher`) for
  every chunk. A wrong code fails the handshake closed — see
  `internal/crypto`.
- **Connection**: tries direct LAN discovery first (UDP multicast beacon,
  `internal/discovery/lan.go`); if that doesn't find a peer within 15s and
  a relay server is configured, falls back to it, trying a brief NAT
  hole-punch upgrade first (`internal/discovery/punch.go`) before settling
  for relayed traffic. The relay only ever forwards already-encrypted
  bytes — it can't read file content.
- **Resumable**: a dropped connection doesn't discard the partial file.
  The receiver tracks the highest verified chunk in a `.part.meta`
  sidecar and the sender resumes from there on reconnect, within the
  code's 5-minute validity window.
- **Folders and compression**: a directory gets packed by a small
  zip-slip-safe archive format (`internal/archive`) and, if it actually
  shrinks the data, compressed with `compress/flate`
  (`internal/compress`) before encryption.
- **Optional QR pairing**: `-qr` on `send` also prints the code as a
  terminal QR code, hand-encoded from scratch (`internal/qr`) — purely a
  convenience layered on the code, which remains the primary pairing path
  either way. See Limits below.

## Quick start

```sh
go build -o bin/parcel ./cmd/parcel
# or: make build
```

On one machine:

```sh
./bin/parcel send ./photo.jpg
# Your code: crimson-otter-lagoon
```

On the other machine (same LAN, or with a relay configured — see below):

```sh
./bin/parcel receive crimson-otter-lagoon
```

A directory works the same way (`./bin/parcel send ./my-folder`); it
arrives unpacked back into a directory on the receiving end.

### Crossing networks

LAN discovery only works when both machines are on the same local
network. To transfer across networks, run a relay somewhere reachable
from both sides:

```sh
./bin/parcel relay -addr :4321
```

then point both `send` and `receive` at it:

```sh
./bin/parcel send ./photo.jpg -relay relay.example.com:4321
./bin/parcel receive crimson-otter-lagoon -relay relay.example.com:4321
```

(or set `PARCEL_RELAY` instead of passing `-relay` each time). `-lan-only`
and `-relay-only` are available if you want to force one path.

On a machine with more than one network adapter (a laptop with Wi-Fi +
Ethernet + VPN, or a VM host with a NAT adapter alongside a host-only one),
LAN discovery's auto-picked interface on each side can disagree, and the
beacon silently never arrives. Pin both sides to the same interface with
`-iface <name-or-ip>` (or `PARCEL_LAN_IFACE`) — e.g. the host-only adapter's
IP when testing between a host machine and a VirtualBox guest.

## Verifying the zero-dependency claim

```sh
go list -m all       # prints only "parcel" — see deps-proof.txt
```

`go.mod` has no `require` block. See `STDLIB.md` for the specific
packages we'd normally have reached for and what stdlib feature replaced
each one.

## Reproducible build

```sh
make reproducible
```

builds the artifact twice (`-trimpath -buildvcs=false`, so the result
doesn't depend on the build path or git state) and diffs the two. Actual
captured run:

```
$ go build -trimpath -buildvcs=false -o bin/parcel-a ./cmd/parcel
$ go build -trimpath -buildvcs=false -o bin/parcel-b ./cmd/parcel
$ sha256sum bin/parcel-a bin/parcel-b
03dc436cad930caf9fbfdb8aa93340ac9e5222c63bd9628cb584c752dd4498e2 *bin/parcel-a
03dc436cad930caf9fbfdb8aa93340ac9e5222c63bd9628cb584c752dd4498e2 *bin/parcel-b
$ cmp bin/parcel-a bin/parcel-b && echo byte-identical
byte-identical
```

also confirmed byte-identical when built from a second, unrelated
directory (see commit history / STDLIB.md for how `-buildvcs=false`
eliminates the git-metadata embedding that would otherwise make this
path-dependent).

## Layout

```
parcel/
├── cmd/parcel/           CLI entrypoint (send/receive/relay subcommands)
├── internal/
│   ├── codeword/         pairing-code generation (crypto/rand wordlist draw)
│   ├── crypto/           X25519 handshake + AES-GCM chunk stream
│   ├── transfer/         resumable chunked wire protocol, timeouts
│   ├── archive/          hand-rolled folder pack/unpack format
│   ├── compress/         compress/flate wrapper with a skip-if-useless heuristic
│   ├── discovery/        LAN multicast discovery, relay server/client, NAT punching
│   ├── source/           prepares a path (archive? compress?) before sending
│   └── qr/               from-scratch QR encoder (+ a decoder, used only for tests)
├── Makefile              build / test / vet / deps-proof / reproducible
├── STDLIB.md             every stdlib-for-package substitution made
└── deps-proof.txt        go list -m all output
```

Tests are colocated `_test.go` files next to the code they cover (Go's
own idiom), not a separate `tests/` tree — every `internal/*` package and
`cmd/parcel` above has one.

## Limits (read before a demo)

- **LAN discovery is implemented per the Go stdlib multicast docs and unit-
  tested at the protocol level, but wasn't end-to-end verified on a real
  LAN in this project's development environment** — the dev machine's
  firewall silently drops the inbound multicast join for unapproved
  binaries, with no admin rights available in that sandbox to add an
  exception. The test suite detects this and skips with a clear message
  rather than falsely passing or failing. Verify on a real network before
  relying on it for a demo.
- **NAT hole punching is best-effort**, not guaranteed. It's split
  deterministically by role (sender dials, receiver accepts) rather than
  racing both directions on both sides — an earlier symmetric version had
  a real bug where that race could let each side connect to a *different*
  physical connection. The current design can't produce that mismatch,
  at the cost of not attempting true bidirectional simultaneous-open, so
  it works on permissive/full-cone NATs and not on symmetric or
  restrictive ones. The relay is always the guaranteed fallback.
- **QR pairing (`-qr`) is verified by round-tripping every encode through
  a matching from-scratch decoder** (see `internal/qr/encode_test.go`),
  which proves internal consistency of the bit packing, Reed-Solomon,
  module placement, and masking — but this sandboxed environment has no
  way to physically scan a code with a phone camera, so that hasn't been
  confirmed against a real scanner. The spoken/typed code is the primary,
  fully-verified pairing method regardless; `-qr` is strictly additive.
- **The relay is an untrusted forwarder by design**, not audited
  infrastructure — it only ever sees already-encrypted bytes and the
  pairing code's handshake still fails closed against an active
  man-in-the-middle, but anyone running a public relay should treat it
  like any other internet-facing service (rate limiting, monitoring, etc.
  aren't implemented here).
- **Compression won't help already-compressed formats** (photos, videos,
  zips) — `internal/source` only keeps a compressed copy if it's
  measurably smaller, so this is a non-issue in practice, but don't
  expect flate to work miracles on JPEGs.

## License

MIT — see `LICENSE`.
