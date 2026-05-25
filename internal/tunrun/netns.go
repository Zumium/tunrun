package tunrun

import (
	"fmt"
	"os"
	"syscall"
)

func currentNetNSID() (string, error) {
	info, err := os.Stat("/proc/self/ns/net")
	if err != nil {
		return "", fmt.Errorf("stat current network namespace: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("stat current network namespace: unexpected stat type %T", info.Sys())
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}

func ensureAnonymousNetNS(hostNetNS string) error {
	if hostNetNS == "" {
		return fmt.Errorf("missing host network namespace identity")
	}
	current, err := currentNetNSID()
	if err != nil {
		return err
	}
	if current == hostNetNS {
		return fmt.Errorf("refusing to configure nftables in the host network namespace")
	}
	return nil
}
