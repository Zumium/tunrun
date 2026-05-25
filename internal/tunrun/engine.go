package tunrun

import (
	"context"
	"time"

	_ "github.com/xjasonlyu/tun2socks/v2/dns"
	"github.com/xjasonlyu/tun2socks/v2/engine"
)

func RunEngine(ctx context.Context, cfg EngineConfig) error {
	engine.Insert(&engine.Key{
		MTU:        cfg.MTU,
		Proxy:      cfg.Proxy,
		Device:     cfg.Device,
		Interface:  cfg.Interface,
		LogLevel:   cfg.LogLevel,
		UDPTimeout: 30 * time.Second,
	})

	engine.Start()
	defer engine.Stop()

	<-ctx.Done()
	return nil
}
