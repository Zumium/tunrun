package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"tunrun/internal/tunrun"
)

const version = "0.1.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		var exitErr tunrun.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}

		fmt.Fprintf(os.Stderr, "tunrun: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 && args[0] == "_engine" {
		return runEngine(args[1:])
	}
	if len(args) > 0 && args[0] == "_exec" {
		return runExec(args[1:])
	}
	if len(args) > 0 && args[0] == "_sudo" {
		return runSudo(args[1:])
	}
	if len(args) > 0 && args[0] == "_netmgr" {
		return runNetMgr(args[1:])
	}

	return runMain(args, "", "", "", true)
}

func runMain(args []string, proxyOverride, proxyOverrideSource, targetPath string, allowElevate bool) error {
	fs := flag.NewFlagSet("tunrun", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var cfg tunrun.Config
	cfg.TargetPath = targetPath
	fs.StringVar(&cfg.ProxyURL, "proxy", "", "upstream proxy URL; defaults to proxy environment variables")
	fs.StringVar(&cfg.Namespace, "ns", "", "network namespace name; default tunrun-<pid>")
	fs.StringVar(&cfg.TunName, "tun", "tun0", "TUN interface name inside the namespace")
	fs.StringVar(&cfg.NamespaceIfName, "ns-if", "eth0", "veth interface name inside the namespace")
	fs.StringVar(&cfg.TunAddress, "tun-address", "198.18.0.1/15", "TUN interface address/prefix")
	fs.StringVar(&cfg.DNS, "dns", "1.1.1.1:53", "DNS server used over TCP through the proxy")
	fs.IntVar(&cfg.MTU, "mtu", 1500, "TUN MTU")
	fs.StringVar(&cfg.LogLevel, "log-level", "warn", "engine log level")
	fs.BoolVar(&cfg.Verbose, "v", false, "print lifecycle commands")
	showVersion := fs.Bool("version", false, "print version")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), usage)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return tunrun.ExitError{Code: 2}
	}

	if *showVersion {
		fmt.Println(version)
		return nil
	}

	if len(fs.Args()) == 0 {
		fs.Usage()
		return tunrun.ExitError{Code: 2}
	}
	if proxyOverride != "" {
		cfg.ProxyURL = proxyOverride
		cfg.ProxySource = proxyOverrideSource
	} else if cfg.ProxyURL == "" {
		proxyURL, proxySource, ok := tunrun.ProxyFromEnvironment(os.Environ())
		if !ok {
			fs.Usage()
			return fmt.Errorf("missing -proxy and no proxy environment variable found")
		}
		cfg.ProxyURL = proxyURL
		cfg.ProxySource = proxySource
	} else {
		cfg.ProxySource = "-proxy"
	}

	if cfg.Namespace == "" {
		cfg.Namespace = fmt.Sprintf("tunrun-%d", os.Getpid())
	}
	if cfg.TunName == "" || cfg.NamespaceIfName == "" {
		return fmt.Errorf("interface names cannot be empty")
	}
	if strings.Contains(cfg.Namespace, "/") {
		return fmt.Errorf("namespace name must not contain '/'")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if os.Geteuid() != 0 && allowElevate {
		return tunrun.ElevateWithSudo(args, cfg.ProxyURL)
	}

	runner := tunrun.NewRunner(cfg)
	return runner.Run(ctx, fs.Args())
}

func runSudo(args []string) error {
	fs := flag.NewFlagSet("tunrun _sudo", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var proxyFile string
	var targetPath string
	fs.StringVar(&proxyFile, "proxy-file", "", "proxy URL file")
	fs.StringVar(&targetPath, "target-path", "", "target command PATH captured before sudo")
	if err := fs.Parse(args); err != nil {
		return tunrun.ExitError{Code: 2}
	}
	if proxyFile == "" {
		return fmt.Errorf("_sudo requires -proxy-file")
	}

	proxyURL, err := tunrun.ReadProxyFile(proxyFile)
	if err != nil {
		return err
	}
	return runMain(fs.Args(), proxyURL, "environment before sudo", targetPath, false)
}

func runEngine(args []string) error {
	fs := flag.NewFlagSet("tunrun _engine", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var cfg tunrun.EngineConfig
	fs.StringVar(&cfg.Device, "device", "", "tun2socks device URI")
	fs.StringVar(&cfg.Proxy, "proxy", "", "proxy URL")
	fs.StringVar(&cfg.Interface, "interface", "", "outbound interface")
	fs.IntVar(&cfg.MTU, "mtu", 1500, "TUN MTU")
	fs.StringVar(&cfg.LogLevel, "log-level", "warn", "engine log level")

	if err := fs.Parse(args); err != nil {
		return tunrun.ExitError{Code: 2}
	}
	if cfg.Device == "" || cfg.Proxy == "" {
		return fmt.Errorf("_engine requires -device and -proxy")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return tunrun.RunEngine(ctx, cfg)
}

func runExec(args []string) error {
	fs := flag.NewFlagSet("tunrun _exec", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var cfg tunrun.ExecConfig
	var groups string
	fs.Int64Var(&cfg.UID, "uid", -1, "target uid")
	fs.Int64Var(&cfg.GID, "gid", -1, "target gid")
	fs.StringVar(&groups, "groups", "", "comma-separated supplementary group ids")

	if err := fs.Parse(args); err != nil {
		return tunrun.ExitError{Code: 2}
	}
	if cfg.UID >= 0 || cfg.GID >= 0 {
		if cfg.UID < 0 || cfg.GID < 0 {
			return fmt.Errorf("_exec requires both -uid and -gid")
		}
		parsedGroups, err := tunrun.ParseGroupList(groups)
		if err != nil {
			return err
		}
		cfg.Groups = parsedGroups
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("_exec requires a command")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	code := tunrun.RunExec(ctx, cfg, fs.Args())
	if code != 0 {
		return tunrun.ExitError{Code: code}
	}
	return nil
}

const usage = `Usage:
  tunrun [-proxy socks5://127.0.0.1:1080] -- command [args...]

Examples:
  ALL_PROXY=socks5://127.0.0.1:1080 tunrun -- curl https://ifconfig.me
  sudo tunrun -proxy socks5://127.0.0.1:1080 -- curl https://ifconfig.me
  sudo tunrun -proxy http://127.0.0.1:7890 -- wget https://example.com/

Options:
`

func init() {
	// Leave a tiny grace period for child shutdown paths that use SIGTERM.
	tunrun.ShutdownGrace = 2 * time.Second
}

func runNetMgr(args []string) error {
	fs := flag.NewFlagSet("tunrun _netmgr", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var cfg tunrun.NetMgrConfig
	var groups string
	fs.StringVar(&cfg.PeerIf, "peer-if", "", "peer interface name")
	fs.StringVar(&cfg.NsIf, "ns-if", "", "namespace interface name")
	fs.StringVar(&cfg.NsCIDR, "ns-cidr", "", "namespace CIDR")
	fs.StringVar(&cfg.HostIP, "host-ip", "", "host IP")
	fs.StringVar(&cfg.HostNetNS, "host-netns", "", "host network namespace identity")
	fs.StringVar(&cfg.DNS, "dns", "", "DNS server")
	fs.StringVar(&cfg.TunName, "tun", "", "tun name")
	fs.StringVar(&cfg.TunAddress, "tun-address", "", "tun address")
	fs.IntVar(&cfg.MTU, "mtu", 1500, "mtu")
	fs.StringVar(&cfg.LogLevel, "log-level", "warn", "log level")
	fs.BoolVar(&cfg.Verbose, "v", false, "verbose")
	fs.StringVar(&cfg.ProxyURL, "proxy", "", "proxy URL")
	fs.Int64Var(&cfg.UID, "uid", -1, "uid")
	fs.Int64Var(&cfg.GID, "gid", -1, "gid")
	fs.StringVar(&groups, "groups", "", "groups")
	fs.StringVar(&cfg.TargetPath, "target-path", "", "target path")

	if err := fs.Parse(args); err != nil {
		return tunrun.ExitError{Code: 2}
	}
	if cfg.UID >= 0 || cfg.GID >= 0 {
		parsedGroups, err := tunrun.ParseGroupList(groups)
		if err != nil {
			return err
		}
		cfg.Groups = parsedGroups
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	code := tunrun.RunNetMgr(ctx, cfg, fs.Args())
	if code != 0 {
		return tunrun.ExitError{Code: code}
	}
	return nil
}
