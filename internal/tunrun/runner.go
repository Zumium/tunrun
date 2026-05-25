package tunrun

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/vishvananda/netlink"
)

type Runner struct {
	cfg Config
}

func NewRunner(cfg Config) *Runner {
	return &Runner{cfg: cfg}
}

func (r *Runner) Run(ctx context.Context, command []string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("run as root to create network namespaces, configure nftables DNS redirect rules, bind DNS on port 53, and open TUN devices")
	}

	proxy, err := ParseProxy(r.cfg.ProxyURL)
	if err != nil {
		return err
	}
	targetIdentity, err := TargetIdentityFromEnvironment(os.Environ())
	if err != nil {
		return err
	}

	netPlan := newNamespaceNetwork(r.cfg)

	// Create host veth
	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: netPlan.hostIf},
		PeerName:  netPlan.peerIf,
	}
	if err := netlink.LinkAdd(veth); err != nil {
		return fmt.Errorf("create veth pair: %w", err)
	}

	defer func() {
		// Clean up host veth, which automatically cleans up peer veth if it's still around
		if link, err := netlink.LinkByName(netPlan.hostIf); err == nil {
			netlink.LinkDel(link)
		}
	}()

	addr, err := netlink.ParseAddr(netPlan.hostCIDR)
	if err != nil {
		return fmt.Errorf("parse host cidr: %w", err)
	}
	hostLink, err := netlink.LinkByName(netPlan.hostIf)
	if err != nil {
		return fmt.Errorf("get host link: %w", err)
	}
	if err := netlink.AddrAdd(hostLink, addr); err != nil {
		return fmt.Errorf("configure host veth address: %w", err)
	}
	if err := netlink.LinkSetUp(hostLink); err != nil {
		return fmt.Errorf("bring host veth up: %w", err)
	}

	var relay *Relay
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), ShutdownGrace)
		defer cancel()
		if relay != nil {
			_ = relay.Close(cleanupCtx)
		}
	}()

	var relayPort int
	relay, relayPort, err = StartRelay(netPlan.hostIP, proxy)
	if err != nil {
		return fmt.Errorf("start proxy relay: %w", err)
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	hostNetNS, err := currentNetNSID()
	if err != nil {
		return err
	}

	if r.cfg.Verbose {
		fmt.Fprintf(os.Stderr, "tunrun: proxy_source=%s namespace=%s target_uid=%s host_if=%s ns_if=%s relay=%s:%d dns=namespace-local:53\n",
			r.cfg.ProxySource, r.cfg.Namespace, targetUIDLabel(targetIdentity), netPlan.hostIf, r.cfg.NamespaceIfName, netPlan.hostIP, relayPort)
	}

	// Prepare netmgr arguments
	netmgrArgs := []string{"_netmgr",
		"-peer-if", netPlan.peerIf,
		"-ns-if", r.cfg.NamespaceIfName,
		"-ns-cidr", netPlan.nsCIDR,
		"-host-ip", netPlan.hostIP,
		"-host-netns", hostNetNS,
		"-dns", r.cfg.DNS,
		"-tun", r.cfg.TunName,
		"-tun-address", r.cfg.TunAddress,
		"-mtu", fmt.Sprint(r.cfg.MTU),
		"-log-level", r.cfg.LogLevel,
		"-proxy", proxy.URLWithEndpoint(netPlan.hostIP, relayPort),
		"-target-path", r.cfg.TargetPath,
	}
	if r.cfg.Verbose {
		netmgrArgs = append(netmgrArgs, "-v")
	}
	if targetIdentity.Valid {
		netmgrArgs = append(netmgrArgs,
			"-uid", fmt.Sprint(targetIdentity.UID),
			"-gid", fmt.Sprint(targetIdentity.GID),
			"-groups", FormatGroupList(targetIdentity.Groups),
		)
	}
	netmgrArgs = append(netmgrArgs, "--")
	netmgrArgs = append(netmgrArgs, command...)

	netmgrCmd := exec.CommandContext(ctx, exe, netmgrArgs...)
	netmgrCmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWNET,
		Pdeathsig:  syscall.SIGKILL,
	}
	netmgrCmd.Stdin = os.Stdin
	netmgrCmd.Stdout = os.Stdout
	netmgrCmd.Stderr = os.Stderr

	if err := netmgrCmd.Start(); err != nil {
		return fmt.Errorf("start netmgr: %w", err)
	}

	// Move peer veth into netmgr's namespace
	peerLink, err := netlink.LinkByName(netPlan.peerIf)
	if err != nil {
		_ = netmgrCmd.Process.Kill()
		return fmt.Errorf("get peer link: %w", err)
	}
	if err := netlink.LinkSetNsPid(peerLink, netmgrCmd.Process.Pid); err != nil {
		_ = netmgrCmd.Process.Kill()
		return fmt.Errorf("move peer veth to netmgr: %w", err)
	}

	err = netmgrCmd.Wait()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			return ExitError{Code: status.ExitStatus()}
		}
	}
	return err
}

func targetUIDLabel(identity TargetIdentity) string {
	if !identity.Valid {
		return "root"
	}
	return fmt.Sprintf("%d", identity.UID)
}

type namespaceNetwork struct {
	hostIf   string
	peerIf   string
	nsIf     string
	hostCIDR string
	nsCIDR   string
	hostIP   string
}

func newNamespaceNetwork(cfg Config) namespaceNetwork {
	id := shortID()
	// Map pid to 169.254.y.z/30 where y is 64..127 to avoid collisions
	// 64 * 256 / 4 = 4096 subnets
	pid := os.Getpid()
	index := pid % 4096
	y := 64 + (index / 64)
	z := (index % 64) * 4
	return namespaceNetwork{
		hostIf:   "trh" + id,
		peerIf:   "trp" + id,
		nsIf:     cfg.NamespaceIfName,
		hostCIDR: fmt.Sprintf("169.254.%d.%d/30", y, z+1),
		nsCIDR:   fmt.Sprintf("169.254.%d.%d/30", y, z+2),
		hostIP:   fmt.Sprintf("169.254.%d.%d", y, z+1),
	}
}

func shortID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	var b strings.Builder
	for i := 0; i < 8; i++ {
		b.WriteByte(alphabet[rand.IntN(len(alphabet))])
	}
	return b.String()
}

func terminateProcess(p *os.Process) {
	if p == nil {
		return
	}
	_ = p.Signal(syscall.SIGTERM)
	time.Sleep(200 * time.Millisecond)
	_ = p.Kill()
}

func terminateProcessGroup(p *os.Process) {
	if p == nil {
		return
	}
	_ = syscall.Kill(-p.Pid, syscall.SIGTERM)
	time.Sleep(200 * time.Millisecond)
	_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
}
