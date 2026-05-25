package tunrun

import "time"

var ShutdownGrace = 2 * time.Second

type Config struct {
	ProxyURL        string
	ProxySource     string
	Namespace       string
	NamespaceIfName string
	TunName         string
	TunAddress      string
	DNS             string
	MTU             int
	LogLevel        string
	TargetPath      string
	Keep            bool
	Verbose         bool
}

type EngineConfig struct {
	Device    string
	Proxy     string
	Interface string
	MTU       int
	LogLevel  string
}

type ExecConfig struct {
	UID    int64
	GID    int64
	Groups []uint32
}

type ExitError struct {
	Code int
}

func (e ExitError) Error() string {
	return "command exited"
}
