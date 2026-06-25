# tunrun

[![Go Version](https://img.shields.io/badge/Go-1.26-blue)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-green)](./LICENSE)

Run any command inside a temporary Linux network namespace, routed through a
proxy — no app-level proxy support needed.

```
  ┌───────────────── host ──────────────────┐
  │  ┌─── anonymous netns ───────────────┐  │
  │  │  target command                    │  │
  │  │  │ default route                   │  │
  │  │  ▼                                 │  │
  │  │  tun2socks ◄── tun0 ──► (TUN)     │  │
  │  │  │                                 │  │
  │  │  ▼                                 │  │
  │  │  veth (ns side)                    │  │
  │  └──┼─────────────────────────────────┘  │
  │     │ veth pair                          │
  │  ┌──┼────────────────────────────────┐   │
  │  │  veth (host side)                 │   │
  │  │  │                                │   │
  │  │  ▼                                │   │
  │  │  relay ────► upstream proxy       │   │
  │  └───────────────────────────────────┘   │
  └──────────────────────────────────────────┘
```

## Quick Start

```sh
go install tunrun/cmd/tunrun@latest

sudo tunrun -proxy socks5://127.0.0.1:1080 -- curl https://ifconfig.me
```

Or use proxy environment variables:

```sh
ALL_PROXY=socks5://127.0.0.1:1080 tunrun -- curl https://ifconfig.me
```

> `tunrun` auto-elevates via `sudo` when run as non-root — no `sudo -E` needed.

## Installation

```sh
go install tunrun/cmd/tunrun@latest
```

Or build from source:

```sh
CGO_ENABLED=0 go build -buildvcs=false -o tunrun ./cmd/tunrun
```

## Usage

```
tunrun [-proxy socks5://127.0.0.1:1080] -- command [args...]
```

### Proxy configuration (resolved in order)

1. CLI flag: `-proxy socks5://127.0.0.1:1080`
2. Config file: `~/.config/tunrun/config.toml`
   ```toml
   proxy = "socks5://127.0.0.1:1080"
   ```
3. Environment: `ALL_PROXY` → `all_proxy` → `HTTPS_PROXY` → `https_proxy` →
   `HTTP_PROXY` → `http_proxy` → `SOCKS_PROXY` → `socks_proxy`

> Proxy variables are **stripped** from the target command's environment to
> force all traffic through the TUN path. The target runs as the sudo caller
> (via `SUDO_UID`/`SUDO_GID`), not as root.

### Options

| Flag | Default | Description |
|------|---------|-------------|
| `-proxy` | _(from env/config)_ | Upstream proxy URL (`socks5://` or `http://`) |
| `-ns` | `tunrun-<pid>` | Network namespace name |
| `-tun` | `tun0` | TUN interface name inside namespace |
| `-ns-if` | `eth0` | veth interface name inside namespace |
| `-tun-address` | `198.18.0.1/15` | TUN interface address/prefix |
| `-dns` | `1.1.1.1:53` | DNS server (queried over TCP through proxy) |
| `-mtu` | `1500` | TUN MTU |
| `-log-level` | `warn` | Engine log level |
| `-v` | `false` | Verbose lifecycle logging |
| `-version` | | Print version |

### Examples

```sh
# SOCKS5 proxy
sudo tunrun -proxy socks5://127.0.0.1:1080 -- curl -I https://www.google.com

# HTTP proxy (TCP + DNS only)
sudo tunrun -proxy http://127.0.0.1:7890 -- wget https://example.com/

# Custom DNS server
sudo tunrun -proxy socks5://127.0.0.1:1080 -dns 8.8.8.8 -- dig example.com

# Verbose mode
sudo tunrun -v -proxy socks5://127.0.0.1:1080 -- curl https://example.com/
```

## Supported Proxies

| Protocol | TCP | UDP | DNS |
|----------|-----|-----|-----|
| SOCKS5 | Yes | Yes (UDP ASSOCIATE) | Yes |
| HTTP CONNECT | Yes | No | Yes |

## How It Works

1. **Network namespace** — an anonymous namespace is created for the target
   command, isolating it from the host network stack.
2. **veth pair** — connects the host and the namespace. The host side runs a
   TCP relay that dials your upstream proxy.
3. **TUN device + tun2socks** — a TUN interface is created inside the
   namespace. The embedded tun2socks engine captures all IP traffic and
   forwards it to the upstream proxy through the veth escape path.
4. **DNS** — nftables rules redirect all UDP/TCP port 53 traffic inside the
   namespace to a local DNS-over-TCP proxy. DNS queries are resolved through
   the configured proxy. Nameservers from `/etc/resolv.conf` are mapped onto
   loopback aliases within the namespace.
5. **Identity** — when invoked via `sudo`, the target command runs as the
   original user (`SUDO_UID`/`SUDO_GID`). Root is only used for namespace,
   TUN, DNS, and cleanup.

## Cleanup

All resources are cleaned up when the target command exits: the tun2socks
engine stops, the host relay closes, the anonymous namespace disappears, and
the host veth link is removed. No `tunrun-*`, `trh*`, or `trp*` leftovers.

## Requirements

- Linux with network namespace support
- Root or `CAP_NET_ADMIN`
- An upstream SOCKS5 or HTTP proxy

## License

[MIT](./LICENSE)
