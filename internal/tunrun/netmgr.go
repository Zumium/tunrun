package tunrun

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/vishvananda/netlink"
)

type NetMgrConfig struct {
	PeerIf     string
	NsIf       string
	NsCIDR     string
	HostIP     string
	HostNetNS  string
	DNS        string
	TunName    string
	TunAddress string
	MTU        int
	LogLevel   string
	Verbose    bool
	ProxyURL   string
	UID        int64
	GID        int64
	Groups     []uint32
	TargetPath string
}

func RunNetMgr(ctx context.Context, cfg NetMgrConfig, command []string) int {
	if err := ensureAnonymousNetNS(cfg.HostNetNS); err != nil {
		fmt.Fprintf(os.Stderr, "tunrun netmgr: %v\n", err)
		return 1
	}

	// 1. Wait for peerIf to be pushed into our namespace by parent
	deadline := time.Now().Add(5 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		if _, err := netlink.LinkByName(cfg.PeerIf); err == nil {
			found = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !found {
		fmt.Fprintf(os.Stderr, "tunrun netmgr: timed out waiting for veth interface %s\n", cfg.PeerIf)
		return 1
	}

	// 2. Configure loopback and veth
	if lo, err := netlink.LinkByName("lo"); err == nil {
		if err := netlink.LinkSetUp(lo); err != nil {
			fmt.Fprintf(os.Stderr, "tunrun netmgr: bring up loopback: %v\n", err)
			return 1
		}
	}
	aliases, err := setupResolverLoopbackAliases("/etc/resolv.conf")
	if err != nil {
		fmt.Fprintf(os.Stderr, "tunrun netmgr: configure resolver aliases: %v\n", err)
		return 1
	}
	if cfg.Verbose && len(aliases) > 0 {
		fmt.Fprintf(os.Stderr, "tunrun netmgr: dns_resolver_aliases=%s\n", strings.Join(aliases, ","))
	}
	dnsListenHost := "127.0.0.1"
	if len(aliases) > 0 {
		dnsListenHost = aliases[0]
	}

	peerLink, _ := netlink.LinkByName(cfg.PeerIf)
	if err := netlink.LinkSetName(peerLink, cfg.NsIf); err != nil {
		fmt.Fprintf(os.Stderr, "tunrun netmgr: rename veth: %v\n", err)
		return 1
	}

	nsLink, _ := netlink.LinkByName(cfg.NsIf)
	addr, err := netlink.ParseAddr(cfg.NsCIDR)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tunrun netmgr: parse ns cidr: %v\n", err)
		return 1
	}
	if err := netlink.AddrAdd(nsLink, addr); err != nil {
		fmt.Fprintf(os.Stderr, "tunrun netmgr: add address to veth: %v\n", err)
		return 1
	}
	if err := netlink.LinkSetUp(nsLink); err != nil {
		fmt.Fprintf(os.Stderr, "tunrun netmgr: bring up veth: %v\n", err)
		return 1
	}

	// 3. Redirect DNS traffic to the namespace-local DNS proxy.
	if err := setupDNSRedirect(dnsListenHost); err != nil {
		fmt.Fprintf(os.Stderr, "tunrun netmgr: configure DNS redirect: %v\n", err)
		return 1
	}

	dnsProxy, err := ParseProxy(cfg.ProxyURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tunrun netmgr: parse DNS proxy URL: %v\n", err)
		return 1
	}
	dnsServer, err := StartDNSServer(dnsListenHost, cfg.DNS, dnsProxy, cfg.Verbose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tunrun netmgr: start DNS proxy: %v\n", err)
		return 1
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), ShutdownGrace)
		defer cancel()
		_ = dnsServer.Close(cleanupCtx)
	}()

	// 4. Start engine
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tunrun netmgr: get executable: %v\n", err)
		return 1
	}

	engineCmd := exec.CommandContext(ctx, exe, "_engine",
		"-device", "tun://"+cfg.TunName,
		"-proxy", cfg.ProxyURL,
		"-interface", cfg.NsIf,
		"-mtu", fmt.Sprint(cfg.MTU),
		"-log-level", cfg.LogLevel,
	)
	engineCmd.Stdout = os.Stderr
	engineCmd.Stderr = os.Stderr
	engineCmd.Env = EnvironmentWithoutProxy(os.Environ())
	engineCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if err := engineCmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "tunrun netmgr: start engine: %v\n", err)
		return 1
	}
	defer func() {
		terminateProcessGroup(engineCmd.Process)
		_ = engineCmd.Wait()
	}()

	// 5. Wait for TUN
	found = false
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := netlink.LinkByName(cfg.TunName); err == nil {
			found = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !found {
		fmt.Fprintf(os.Stderr, "tunrun netmgr: timed out waiting for %s\n", cfg.TunName)
		return 1
	}

	// 6. Configure TUN
	tunLink, _ := netlink.LinkByName(cfg.TunName)
	tunAddr, err := netlink.ParseAddr(cfg.TunAddress)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tunrun netmgr: parse tun address: %v\n", err)
		return 1
	}
	if err := netlink.AddrReplace(tunLink, tunAddr); err != nil {
		fmt.Fprintf(os.Stderr, "tunrun netmgr: replace tun address: %v\n", err)
		return 1
	}
	if err := netlink.LinkSetUp(tunLink); err != nil {
		fmt.Fprintf(os.Stderr, "tunrun netmgr: bring up tun: %v\n", err)
		return 1
	}

	_, defaultNet, _ := net.ParseCIDR("0.0.0.0/0")
	route := &netlink.Route{
		LinkIndex: tunLink.Attrs().Index,
		Scope:     netlink.SCOPE_UNIVERSE,
		Dst:       defaultNet,
	}
	if err := netlink.RouteReplace(route); err != nil {
		fmt.Fprintf(os.Stderr, "tunrun netmgr: replace default route: %v\n", err)
		return 1
	}

	// 7. Run target command via _exec
	execArgs := []string{"_exec"}
	if cfg.UID >= 0 || cfg.GID >= 0 {
		execArgs = append(execArgs,
			"-uid", fmt.Sprint(cfg.UID),
			"-gid", fmt.Sprint(cfg.GID),
			"-groups", FormatGroupList(cfg.Groups),
		)
	}
	execArgs = append(execArgs, "--")
	execArgs = append(execArgs, command...)

	cmd := exec.CommandContext(ctx, exe, execArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// The environment needs to be constructed.
	// Wait, targetIdentity is just UID, GID, Groups.
	identity := TargetIdentity{
		Valid:  cfg.UID >= 0 || cfg.GID >= 0,
		UID:    uint32(cfg.UID),
		GID:    uint32(cfg.GID),
		Groups: cfg.Groups,
	}
	identity.Fill()
	cmd.Env = TargetEnvironmentWithPath(os.Environ(), identity, cfg.TargetPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "tunrun netmgr: start command: %v\n", err)
		return 127
	}

	err = cmd.Wait()
	if err == nil {
		return 0
	}
	if ctx.Err() != nil {
		terminateProcessGroup(cmd.Process)
		return 130
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			return status.ExitStatus()
		}
	}
	fmt.Fprintf(os.Stderr, "tunrun netmgr: command failed: %v\n", err)
	return 1
}

func setupResolverLoopbackAliases(resolvPath string) ([]string, error) {
	nameservers, err := readResolvConfNameservers(resolvPath)
	if err != nil {
		return nil, err
	}

	lo, err := netlink.LinkByName("lo")
	if err != nil {
		return nil, err
	}

	aliases := make([]string, 0, len(nameservers))
	for _, addr := range nameservers {
		if !addr.Is4() || addr.IsLoopback() || addr.IsUnspecified() {
			continue
		}
		if err := addLoopbackIPv4Alias(lo, addr); err != nil {
			return nil, fmt.Errorf("add %s to loopback: %w", addr, err)
		}
		aliases = append(aliases, addr.String())
	}
	return aliases, nil
}

func addLoopbackIPv4Alias(link netlink.Link, addr netip.Addr) error {
	ip := net.IP(append([]byte(nil), addr.AsSlice()...))
	err := netlink.AddrAdd(link, &netlink.Addr{
		IPNet: &net.IPNet{
			IP:   ip,
			Mask: net.CIDRMask(32, 32),
		},
		Scope: int(netlink.SCOPE_HOST),
	})
	if errors.Is(err, syscall.EEXIST) {
		return nil
	}
	return err
}
