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
)

type Runner struct {
	cfg Config
}

func NewRunner(cfg Config) *Runner {
	return &Runner{cfg: cfg}
}

func (r *Runner) Run(ctx context.Context, command []string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("run as root to create network namespaces, write /etc/netns, bind DNS on port 53, and open TUN devices")
	}

	if _, err := exec.LookPath("ip"); err != nil {
		return fmt.Errorf("ip command not found: install iproute2")
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
	if err := setupNamespace(ctx, r.cfg, netPlan); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), ShutdownGrace)
		defer cancel()
		removeNamespace(cleanupCtx, r.cfg, r.cfg.Namespace)
		removeLink(cleanupCtx, r.cfg, netPlan.hostIf)
		removeLink(cleanupCtx, r.cfg, netPlan.peerIf)
		return err
	}

	var (
		dnsServer  *DNSServer
		relay      *Relay
		engineCmd  *exec.Cmd
		resolvPath string
	)
	defer func() {
		if engineCmd != nil && engineCmd.Process != nil {
			terminateProcessGroup(engineCmd.Process)
			_ = engineCmd.Wait()
		}

		cleanupCtx, cancel := context.WithTimeout(context.Background(), ShutdownGrace)
		defer cancel()

		if relay != nil {
			_ = relay.Close(cleanupCtx)
		}
		if dnsServer != nil {
			_ = dnsServer.Close(cleanupCtx)
		}

		if r.cfg.Keep {
			fmt.Fprintf(os.Stderr, "tunrun: kept namespace %s\n", r.cfg.Namespace)
			return
		}

		removeNetnsResolvConf(resolvPath)
		removeNamespace(cleanupCtx, r.cfg, r.cfg.Namespace)
		removeLink(cleanupCtx, r.cfg, netPlan.hostIf)
	}()

	dnsServer, err = StartDNSServer(netPlan.hostIP, r.cfg.DNS, proxy)
	if err != nil {
		return fmt.Errorf("start DNS proxy: %w", err)
	}

	resolvPath, err = writeNetnsResolvConf(r.cfg.Namespace, netPlan.hostIP)
	if err != nil {
		return fmt.Errorf("write namespace resolv.conf: %w", err)
	}

	var relayPort int
	relay, relayPort, err = StartRelay(netPlan.hostIP, proxy.Host, proxy.Port)
	if err != nil {
		return fmt.Errorf("start proxy relay: %w", err)
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	if r.cfg.Verbose {
		fmt.Fprintf(os.Stderr, "tunrun: proxy_source=%s namespace=%s target_uid=%s host_if=%s ns_if=%s relay=%s:%d dns=%s:53\n",
			r.cfg.ProxySource, r.cfg.Namespace, targetUIDLabel(targetIdentity), netPlan.hostIf, r.cfg.NamespaceIfName, netPlan.hostIP, relayPort, netPlan.hostIP)
	}

	engineCmd = exec.CommandContext(ctx,
		"ip", "netns", "exec", r.cfg.Namespace,
		exe, "_engine",
		"-device", "tun://"+r.cfg.TunName,
		"-proxy", proxy.URLWithEndpoint(netPlan.hostIP, relayPort),
		"-interface", r.cfg.NamespaceIfName,
		"-mtu", fmt.Sprint(r.cfg.MTU),
		"-log-level", r.cfg.LogLevel,
	)
	engineCmd.Stdout = os.Stderr
	engineCmd.Stderr = os.Stderr
	engineCmd.Env = EnvironmentWithoutProxy(os.Environ())
	engineCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := engineCmd.Start(); err != nil {
		return fmt.Errorf("start embedded tun2socks engine: %w", err)
	}

	if err := r.waitForTun(ctx); err != nil {
		return err
	}

	if err := r.configureTun(ctx); err != nil {
		return err
	}

	appCode := r.runCommand(ctx, command, targetIdentity)

	if appCode != 0 {
		return ExitError{Code: appCode}
	}
	return nil
}

func (r *Runner) configureTun(ctx context.Context) error {
	ip := ipTool{verbose: r.cfg.Verbose}
	if err := ip.run(ctx, "netns", "exec", r.cfg.Namespace, "ip", "addr", "replace", r.cfg.TunAddress, "dev", r.cfg.TunName); err != nil {
		return fmt.Errorf("configure TUN address: %w", err)
	}
	if err := ip.run(ctx, "netns", "exec", r.cfg.Namespace, "ip", "link", "set", r.cfg.TunName, "up"); err != nil {
		return fmt.Errorf("bring TUN up: %w", err)
	}
	if err := ip.run(ctx, "netns", "exec", r.cfg.Namespace, "ip", "route", "replace", "default", "dev", r.cfg.TunName); err != nil {
		return fmt.Errorf("set namespace default route: %w", err)
	}
	return nil
}

func (r *Runner) runCommand(ctx context.Context, args []string, identity TargetIdentity) int {
	execArgs := []string{"netns", "exec", r.cfg.Namespace}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tunrun: locate executable: %v\n", err)
		return 127
	}
	execArgs = append(execArgs, exe, "_exec")
	if identity.Valid {
		execArgs = append(execArgs,
			"-uid", fmt.Sprint(identity.UID),
			"-gid", fmt.Sprint(identity.GID),
			"-groups", FormatGroupList(identity.Groups),
		)
	}
	execArgs = append(execArgs, "--")
	execArgs = append(execArgs, args...)

	cmd := exec.CommandContext(ctx, "ip", execArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = TargetEnvironment(os.Environ(), identity)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "tunrun: start command: %v\n", err)
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
	fmt.Fprintf(os.Stderr, "tunrun: command failed: %v\n", err)
	return 1
}

func targetUIDLabel(identity TargetIdentity) string {
	if !identity.Valid {
		return "root"
	}
	return fmt.Sprintf("%d", identity.UID)
}

func (r *Runner) waitForTun(ctx context.Context) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		check := exec.CommandContext(ctx, "ip", "netns", "exec", r.cfg.Namespace, "ip", "link", "show", r.cfg.TunName)
		if check.Run() == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("timed out waiting for %s; check embedded engine logs above", r.cfg.TunName)
}

func newNamespaceNetwork(cfg Config) namespaceNetwork {
	id := shortID()
	octet := 64 + rand.IntN(128)
	return namespaceNetwork{
		nsName:   cfg.Namespace,
		hostIf:   "trh" + id,
		peerIf:   "trp" + id,
		nsIf:     cfg.NamespaceIfName,
		hostCIDR: fmt.Sprintf("169.254.%d.1/30", octet),
		nsCIDR:   fmt.Sprintf("169.254.%d.2/30", octet),
		hostIP:   fmt.Sprintf("169.254.%d.1", octet),
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
