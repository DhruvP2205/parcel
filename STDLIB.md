# STDLIB.md — what we'd normally have imported

Every entry below is a real package a Go developer would reasonably reach
for to build this tool, and the standard-library feature actually used
instead. `go.mod` has no `require` block; `go list -m all` prints only
`parcel` (see `deps-proof.txt`).

1. **A "simple crypto box" library** (e.g. `golang.org/x/crypto/nacl/box`,
   or a wrapper package promising "authenticated encryption in one call")
   — replaced by hand-composing `crypto/ecdh` (X25519 key exchange) with
   `crypto/aes` + `crypto/cipher` (AES-256-GCM) ourselves in
   `internal/crypto/handshake.go` and `stream.go`. We compose primitives,
   we don't invent a cipher.

2. **`golang.org/x/crypto/hkdf`** — HKDF isn't in the standard library, so
   we hand-rolled HKDF-Extract/Expand (RFC 5869) from `crypto/hmac` +
   `crypto/sha256` in `internal/crypto/handshake.go`. This is also where
   the pairing code gets folded in: it's used as the HKDF salt against the
   ECDH shared secret, not just a public info label, so an active
   man-in-the-middle can't complete key confirmation without knowing it.

3. **A PAKE library** (e.g. a Go SPAKE2/OPAQUE implementation) — we don't
   implement a formally-proven PAKE. Instead, key confirmation (see #2) is
   authenticated by the shared code; documented in code comments as
   "PAKE-lite," an honest, narrower claim than a real PAKE gives you.

4. **`golang.org/x/crypto/pbkdf2`** — used to be the only way to get
   PBKDF2 in Go; it's now `crypto/pbkdf2` (RFC 8018) as of Go 1.24, used
   in `internal/discovery/lan.go` to stretch the LAN discovery beacon's
   tag so it resists offline brute-forcing by anyone passively listening
   on the local network.

5. **A QR code generation library** (e.g. `github.com/skip2/go-qrcode`) —
   replaced by `internal/qr`, a from-scratch QR encoder (GF(256) Reed-
   Solomon error correction, module placement, all 8 mask patterns with
   spec-accurate penalty scoring, BCH-protected format info) built on
   nothing but slices and bit arithmetic. See the Package Killer section
   below.

6. **`archive/zip`, or a third-party archiver** — for whole-folder
   transfers we needed a simple, streaming, zip-slip-safe format that
   plugs directly into the resumable encrypted chunk pipeline, not general
   ZIP compatibility. `internal/archive` hand-rolls a flat, framed entry
   stream instead (`filepath.WalkDir` to build it, per-segment `..`
   rejection to unpack it safely).

7. **A diceware/passphrase-generator package** — `internal/codeword`
   draws pairing-code words using `crypto/rand` via `math/big.Int`
   (rejection sampling handled internally, avoiding modulo bias) against a
   fixed 192-word list, no external wordlist package.

8. **`gorilla/websocket` or a signaling/rendezvous library** — the relay
   server (`internal/discovery/relay_server.go`) is plain framed TCP over
   `net.Listen`/`net.Dial`. It only ever forwards already-encrypted bytes,
   so it doesn't need a message-framed protocol library, just length-
   prefixed reads.

9. **`pion/webrtc` or an ICE/NAT-traversal library** — NAT hole punching
   (`internal/discovery/punch.go`) is hand-rolled: the relay's pairing
   handshake reveals each peer's observed public address, and the CLI
   attempts a direct `net.Dial`/`net.Listener.Accept` before falling back
   to relaying. No ICE, no STUN/TURN client — see the README for exactly
   how far this best-effort mechanism reaches.

10. **An mDNS/service-discovery library** (e.g. `hashicorp/mdns`,
    `grandcat/zeroconf`) — LAN peer discovery (`internal/discovery/lan.go`)
    is a small custom UDP multicast beacon via `net.ListenMulticastUDP`,
    tagged with the PBKDF2-stretched code (#4) instead of a general-purpose
    service-advertisement protocol we don't need.

11. **A compression library** (e.g. `klauspost/compress`) — plain
    `compress/flate`, wrapped in `internal/compress` with a "only use it
    if it actually shrinks the data by ≥2%" heuristic, since flate doesn't
    help already-compressed formats and we'd rather say so than pretend
    otherwise.

12. **A CLI framework** (`cobra`, `urfave/cli`) — `flag.NewFlagSet` per
    subcommand plus a small manual dispatch switch in `cmd/parcel/main.go`.
    Four subcommands and a handful of flags don't need a framework.

13. **A retry/backoff utility package** — the receiver's reconnect loop in
    `cmd/parcel/main.go` is a plain hand-rolled exponential backoff
    (`time.Sleep` doubling up to a cap), not worth a dependency.

14. **`golang.org/x/sync/errgroup` or similar concurrency helpers** — the
    LAN-vs-relay-vs-punch connection races in `cmd/parcel/main.go` use
    plain goroutines and buffered channels (`select` over multiple
    outcome channels), which is what `errgroup` would compile down to
    here anyway.

15. **`github.com/fatih/color`, `github.com/mattn/go-colorable`, and
    `github.com/mattn/go-isatty`** — `internal/ansi` hand-rolls the whole
    stack: raw SGR escape constants, a terminal check per OS (`os.Stat`'s
    `ModeCharDevice` bit on Unix; `GetConsoleMode`/`SetConsoleMode` via
    `syscall.NewLazyDLL("kernel32.dll")` on Windows, which also opts a
    legacy `cmd.exe`/PowerShell console into VT processing), and respects
    the `NO_COLOR` convention. No terminal library, no ANSI-code package.

## Package Killer

**Replaces:** `github.com/skip2/go-qrcode` (and equivalents like
`github.com/yeqown/go-qrcode`) — widely-used, actively-maintained Go QR
code generation packages.

**With:** `internal/qr`, implemented from ISO/IEC 18004 directly: GF(256)
arithmetic and Reed-Solomon error correction (`rs.go`), finder/timing/
alignment/format-info module placement and the standard zigzag data order
(`matrix.go`), all 8 mask patterns with the spec's 4-rule penalty scoring
(`penalty.go`), alphanumeric bit-packing and BCH format-info encoding
(`encode.go`), and terminal rendering via Unicode half-blocks
(`render.go`).

**Honest scope:** deliberately narrow — versions 1-2, error-correction
level L, Alphanumeric mode only — because that's all any pairing code
this tool generates ever needs (worst case 29 characters, version 2-L
holds 47). A general-purpose replacement for `go-qrcode` would need every
version (1-40), all four ECC levels, and byte/kanji modes too; we didn't
build that. Verified via a matching from-scratch decoder round-tripping
every encode in `internal/qr/encode_test.go` (empty string, every
alphanumeric character, both version-capacity boundaries, and realistic
worst-case pairing codes) — not verified against a real phone camera scan
in this development environment. See README.md's Limits section.
