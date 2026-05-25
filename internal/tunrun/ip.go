package tunrun

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type ipTool struct {
	verbose bool
}

func (i ipTool) run(ctx context.Context, args ...string) error {
	if i.verbose {
		fmt.Fprintf(os.Stderr, "+ ip %v\n", args)
	}
	cmd := exec.CommandContext(ctx, "ip", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

type namespaceNetwork struct {
	nsName   string
	hostIf   string
	peerIf   string
	nsIf     string
	hostCIDR string
	nsCIDR   string
	hostIP   string
}

func setupNamespace(ctx context.Context, cfg Config, n namespaceNetwork) error {
	ip := ipTool{verbose: cfg.Verbose}

	if err := ip.run(ctx, "netns", "add", n.nsName); err != nil {
		return fmt.Errorf("create netns %s: %w", n.nsName, err)
	}

	if err := ip.run(ctx, "link", "add", n.hostIf, "type", "veth", "peer", "name", n.peerIf); err != nil {
		return fmt.Errorf("create veth pair: %w", err)
	}
	if err := ip.run(ctx, "addr", "add", n.hostCIDR, "dev", n.hostIf); err != nil {
		return fmt.Errorf("configure host veth address: %w", err)
	}
	if err := ip.run(ctx, "link", "set", n.hostIf, "up"); err != nil {
		return fmt.Errorf("bring host veth up: %w", err)
	}
	if err := ip.run(ctx, "link", "set", n.peerIf, "netns", n.nsName); err != nil {
		return fmt.Errorf("move peer veth to netns: %w", err)
	}
	if err := ip.run(ctx, "netns", "exec", n.nsName, "ip", "link", "set", "lo", "up"); err != nil {
		return fmt.Errorf("bring namespace loopback up: %w", err)
	}
	if err := ip.run(ctx, "netns", "exec", n.nsName, "ip", "link", "set", n.peerIf, "name", n.nsIf); err != nil {
		return fmt.Errorf("rename namespace veth: %w", err)
	}
	if err := ip.run(ctx, "netns", "exec", n.nsName, "ip", "addr", "add", n.nsCIDR, "dev", n.nsIf); err != nil {
		return fmt.Errorf("configure namespace veth address: %w", err)
	}
	if err := ip.run(ctx, "netns", "exec", n.nsName, "ip", "link", "set", n.nsIf, "up"); err != nil {
		return fmt.Errorf("bring namespace veth up: %w", err)
	}

	return nil
}

func removeNamespace(ctx context.Context, cfg Config, ns string) {
	_ = ipTool{verbose: cfg.Verbose}.run(ctx, "netns", "del", ns)
}

func removeLink(ctx context.Context, cfg Config, name string) {
	if name == "" {
		return
	}
	_ = ipTool{verbose: cfg.Verbose}.run(ctx, "link", "del", name)
}

func writeNetnsResolvConf(nsName, nameserver string) (string, error) {
	dir := filepath.Join("/etc/netns", nsName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	path := filepath.Join(dir, "resolv.conf")
	body := []byte(fmt.Sprintf("nameserver %s\noptions edns0 trust-ad\n", nameserver))
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func removeNetnsResolvConf(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
	_ = os.Remove(filepath.Dir(path))
}
