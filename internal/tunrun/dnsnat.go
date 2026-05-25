package tunrun

import (
	"fmt"
	"net"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

func setupDNSRedirect(listenIP string) error {
	ip := net.ParseIP(listenIP).To4()
	if ip == nil {
		return fmt.Errorf("DNS listen IP %q is not IPv4", listenIP)
	}

	conn, err := nftables.New()
	if err != nil {
		return err
	}

	table := conn.AddTable(&nftables.Table{
		Family: nftables.TableFamilyIPv4,
		Name:   "tunrun_dns",
	})
	chain := conn.AddChain(&nftables.Chain{
		Table:    table,
		Name:     "output",
		Type:     nftables.ChainTypeNAT,
		Hooknum:  nftables.ChainHookOutput,
		Priority: nftables.ChainPriorityNATDest,
	})

	conn.AddRule(dnsRedirectRule(table, chain, unix.IPPROTO_UDP, ip))
	conn.AddRule(dnsRedirectRule(table, chain, unix.IPPROTO_TCP, ip))

	if err := conn.Flush(); err != nil {
		return fmt.Errorf("apply nftables DNS redirect rules: %w", err)
	}
	return nil
}

func dnsRedirectRule(table *nftables.Table, chain *nftables.Chain, proto byte, listenIP []byte) *nftables.Rule {
	return &nftables.Rule{
		Table: table,
		Chain: chain,
		Exprs: []expr.Any{
			&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
			&expr.Cmp{
				Op:       expr.CmpOpEq,
				Register: 1,
				Data:     []byte{proto},
			},
			&expr.Payload{
				DestRegister: 1,
				Base:         expr.PayloadBaseTransportHeader,
				Offset:       2,
				Len:          2,
			},
			&expr.Cmp{
				Op:       expr.CmpOpEq,
				Register: 1,
				Data:     binaryutil.BigEndian.PutUint16(53),
			},
			&expr.Immediate{
				Register: 1,
				Data:     listenIP,
			},
			&expr.Immediate{
				Register: 2,
				Data:     binaryutil.BigEndian.PutUint16(53),
			},
			&expr.NAT{
				Type:        expr.NATTypeDestNAT,
				Family:      unix.NFPROTO_IPV4,
				RegAddrMin:  1,
				RegProtoMin: 2,
			},
		},
	}
}
