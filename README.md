# tunrun

`tunrun` runs a command in a temporary Linux network namespace and sends its
traffic through a proxy, even when the command itself has no proxy support.

It is a single Go binary. It creates:

- a dedicated network namespace for the app
- a veth link between the host and that namespace
- a host-side relay to your upstream HTTP or SOCKS5 proxy
- an embedded tun2socks engine inside the namespace

The app sees a normal network stack whose default route points at the TUN
interface. The embedded engine receives IP traffic from the TUN interface and
dials the upstream proxy through the veth escape path.

## Requirements

- Linux with network namespace support
- root or `CAP_NET_ADMIN`
- `ip` from iproute2
- an upstream `socks5://` or `http://` proxy reachable from the host

## Build

```sh
CGO_ENABLED=0 go build -buildvcs=false -o tunrun ./cmd/tunrun
```

## Usage

```sh
ALL_PROXY=socks5://127.0.0.1:1080 ./tunrun -- curl https://ifconfig.me
sudo ./tunrun -proxy socks5://127.0.0.1:1080 -- curl https://ifconfig.me
sudo ./tunrun -proxy http://127.0.0.1:7890 -- wget https://example.com/
```

If `-proxy` is omitted, `tunrun` reads the first non-empty value from:
`ALL_PROXY`, `all_proxy`, `HTTPS_PROXY`, `https_proxy`, `HTTP_PROXY`,
`http_proxy`, `SOCKS_PROXY`, `socks_proxy`.

The target command runs with those proxy variables removed from its environment
so traffic is forced through the namespace TUN path instead of app-level proxy
settings.

When `tunrun` is started through `sudo`, the target command is executed as the
original sudo caller from `SUDO_UID`/`SUDO_GID`, not as root. The root process is
kept only for namespace, TUN, DNS, relay, and cleanup work.

If `tunrun` is started without root privileges, it resolves the proxy first and
then re-runs itself through `sudo`. You do not need `sudo -E`.

Useful options:

```sh
sudo ./tunrun -v -proxy socks5://127.0.0.1:1080 -- curl https://example.com/
sudo ./tunrun -keep -proxy socks5://127.0.0.1:1080 -- bash
```

`-keep` leaves the namespace in place for inspection.

## Cleanup

By default, `tunrun` cleans up after the target command exits. It stops the
embedded engine, closes the DNS proxy and host relay, removes the namespace
`resolv.conf`, deletes the network namespace, and removes the host veth link.

Use `-keep` only when you want to inspect the namespace after exit.

## Current scope

`tunrun` targets TCP applications plus DNS, and generic UDP is supported when
the upstream proxy is SOCKS5 and permits UDP ASSOCIATE. It writes a
namespace-specific `resolv.conf` and runs a small DNS-over-TCP proxy on the host
veth address, so DNS queries are also resolved through the configured proxy.

HTTP proxies still cover TCP plus DNS only because plain HTTP CONNECT does not
provide a generic UDP relay.
