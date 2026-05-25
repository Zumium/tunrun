# AGENTS.md

Guidance for agents working on this repository.

## Project Shape

`tunrun` is a Go CLI that runs an application inside a temporary Linux network
namespace and routes its traffic through a proxy via an embedded tun2socks
engine. It is intended to build as a single static binary.

Key paths:

- `cmd/tunrun/main.go`: CLI entrypoint and hidden internal subcommands.
- `internal/tunrun/runner.go`: namespace lifecycle and target command launch.
- `internal/tunrun/ip.go`: `iproute2` namespace/veth/resolv.conf helpers.
- `internal/tunrun/engine.go`: embedded tun2socks engine.
- `internal/tunrun/sudo.go`: automatic sudo re-exec path.
- `internal/tunrun/exec.go`: namespace-internal target command launcher.
- `internal/tunrun/identity.go`: sudo caller UID/GID/group detection.
- `internal/tunrun/proxy.go`: proxy parsing and environment handling.
- `internal/tunrun/dns.go`: host-side DNS-over-TCP proxy.

## Commands

Use writable Go caches because the default home cache may be read-only in the
agent sandbox:

```sh
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./...
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go vet ./...
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod CGO_ENABLED=0 go build -buildvcs=false -o tunrun ./cmd/tunrun
```

Run `gofmt` on touched Go files before testing.

## Integration Testing

Full behavior requires root privileges because the tool creates network
namespaces, veth links, TUN devices, `/etc/netns/<ns>/resolv.conf`, and a DNS
listener on port 53.

Preferred integration smoke test:

```sh
ALL_PROXY=http://127.0.0.1:7890 ./tunrun -v -- curl -I -L --max-time 30 https://www.google.com
```

This should:

- auto re-exec through `sudo` without requiring `sudo -E`
- show `proxy_source=environment before sudo`
- run the target command as the sudo caller, not root
- return `HTTP/2 200`
- clean up the namespace and host veth after exit

Cleanup checks:

```sh
sudo ip netns list
sudo ip -o link show
sudo find /etc/netns -maxdepth 2 -path '/etc/netns/tunrun-*' -print
sudo find /run/netns -maxdepth 1 -name 'tunrun-*' -print
```

There should be no `tunrun-*`, `trh*`, or `trp*` leftovers after a normal run.

## Design Constraints

- Keep the deliverable as one binary. Do not add runtime dependencies on
  external proxy engines such as `sing-box`.
- Keep `ip` from iproute2 as the only expected system command dependency.
- The root process may configure networking, but the target application must run
  as the sudo caller when `SUDO_UID`/`SUDO_GID` are present.
- Do not pass proxy environment variables into the target command. The app
  should be forced through the namespace TUN path.
- Preserve automatic cleanup on all error paths. If a resource is created, make
  sure it is registered for deferred cleanup.
- `-keep` is for inspection and may intentionally leave the namespace around.

## Current Scope

The first version targets TCP applications plus DNS. Generic UDP forwarding is
supported for SOCKS5 upstream proxies that permit UDP ASSOCIATE; HTTP upstream
proxies still cover TCP plus DNS only.
