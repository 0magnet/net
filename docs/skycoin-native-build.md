# Building Skycoin/Skywire native binaries with TinyGo + this net fork

This fork's `netdev_native.go` registers a default **host netdev** backed by raw
Linux syscalls, so a TinyGo-compiled **native Linux** binary can use `net.Dial`,
`net.Listen`, and DNS lookups with no driver and no host setup. This doc records
**how to make stock TinyGo actually use this fork** — verified, with the dead
ends noted so nobody re-derives them.

Related: [skycoin/skycoin#2902](https://github.com/skycoin/skycoin/issues/2902),
[skycoin/skywire#2051](https://github.com/skycoin/skywire/issues/2051).

## TL;DR

Install **stock** TinyGo (0.41.1+) — you do **not** need to fork or rebuild the
compiler. Overlay this repo over TinyGo's bundled `net` package with a bind
mount, then build:

```sh
# CI (GitHub Actions runner is root):
sudo mount --bind  /path/to/this/net  "$(tinygo env TINYGOROOT)/src/net"
tinygo build -o app .

# Local / unprivileged — no sudo, no password. The mount lives only inside the
# new namespace and disappears when the command exits; your real TINYGOROOT is
# never modified:
unshare -rm bash -c '
  mount --bind /path/to/this/net "$(tinygo env TINYGOROOT)/src/net"
  tinygo build -o app .'
```

`unshare -r` maps your user to root **inside a throwaway user namespace**; `-m`
gives that namespace its own mount table. Bind-mounting there needs no real
privilege and is invisible to the rest of the system.

## Why a bind mount (and not something simpler)

TinyGo resolves the `net` import from `$TINYGOROOT/src/net`, then builds a
**cached merged GOROOT** by overlaying `$TINYGOROOT/src` onto the standard Go
GOROOT. Two consequences drive the approach:

1. A stock install ships `src/net` as **vendored, root-owned files** (e.g.
   `/usr/lib/tinygo/src/net`), not a git submodule you can `git checkout`. The
   "swap the submodule" method only applies if you build TinyGo *from a source
   checkout*.
2. The goroot merge needs **real directories** under `src/` so it can recurse
   and overlay.

A bind mount replaces only `net` while leaving the rest of the root a pristine,
real-directory tree — so the merge works and nothing else is disturbed.

### Methods that do NOT work (don't waste time on these)

| Attempt | Result |
|---|---|
| Symlink-farm `TINYGOROOT` (symlink each `src/*` child, point `net` at the fork) | **Fails.** Goroot merge chokes on symlinked dirs: `symlink .../src/testing: file exists`. It needs real dirs to recurse into. |
| Hardlink-farm the root (`cp -al`) | **Fails** for an unprivileged user: root-owned files + `fs.protected_hardlinks` → `Operation not permitted`. |
| Point `TINYGOROOT` at garbage | Rejected — TinyGo validates the root (`$TINYGOROOT was not set to the correct root`). |
| Copy the whole root and edit `net` | Works but wasteful — the root is ~1.2 GB. The bind mount avoids the copy entirely. |

## Verification (what "it works" means)

Built with stock TinyGo 0.41.1, this fork bind-mounted over `src/net`. A
TinyGo-compiled binary performed a real bidirectional TCP round-trip against a
separate host listener:

```
HOST GOT: ping
CLIENT GOT: pong
```

Control — the **same** program built with **stock** `net` (no fork):

```
DIAL ERR: Lookup of host name '127.0.0.1' failed: Netdev not set
```

That is the whole point: stock TinyGo has no default netdev; this fork adds one.

## CI sketch (native lane)

```yaml
  native-tinygo:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with: { go-version: '1.26.4', cache: true }
      - uses: acifani/setup-tinygo@v2
        with: { tinygo-version: '0.41.1' }
      - name: Overlay the host-netdev net fork
        run: |
          git clone --depth 1 -b native-netdev-ipv6 \
            https://github.com/0magnet/net /tmp/tinygo-net
          sudo mount --bind /tmp/tinygo-net "$(tinygo env TINYGOROOT)/src/net"
      - name: Build the daemon with TinyGo
        run: tinygo build -o /dev/null .
```

> Note: this proves **`net`** works. Compiling the full daemon additionally
> depends on the build-tagged `_tinygo.go` shims in the app repos (pprof, tls,
> gin/http3, cobra templates, etc.). See `skycoin/skywire`
> `docs/design/tinygo-dmsg-client.md` for the wasm/IoT (net/http-free) regime,
> which uses **stock** TinyGo with no fork at all.

## The netdev itself

`netdev_native.go` — `//go:build linux && !baremetal && !nintendoswitch &&
!wasm_unknown && !tinygo.wasm`. Its `init()` calls `useNetdev(&hostNetdev{})`,
implementing the `netdever` interface (Socket/Bind/Connect/Listen/Accept/
Send/Recv/Close/SetSockOpt/GetHostByName) directly on `syscall.Socket` etc.,
which TinyGo lowers to real inline-asm syscalls on the native linux target.
IPv6 support and the `lookup.go` changes are in the follow-up commit.
