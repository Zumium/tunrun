package tunrun

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type DNSServer struct {
	pc       net.PacketConn
	upstream string
	proxy    Proxy
	wg       sync.WaitGroup
}

func StartDNSServer(listenHost, upstream string, proxy Proxy) (*DNSServer, error) {
	host, port, err := ParseDNS(upstream)
	if err != nil {
		return nil, err
	}

	pc, err := net.ListenPacket("udp", net.JoinHostPort(listenHost, "53"))
	if err != nil {
		return nil, err
	}

	s := &DNSServer{
		pc:       pc,
		upstream: net.JoinHostPort(host, strconv.Itoa(port)),
		proxy:    proxy,
	}
	s.wg.Add(1)
	go s.serve()
	return s, nil
}

func (s *DNSServer) Close(ctx context.Context) error {
	_ = s.pc.Close()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (s *DNSServer) serve() {
	defer s.wg.Done()
	buf := make([]byte, 4096)
	for {
		n, addr, err := s.pc.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}

		query := make([]byte, n)
		copy(query, buf[:n])
		s.wg.Add(1)
		go s.handle(addr, query)
	}
}

func (s *DNSServer) handle(addr net.Addr, query []byte) {
	defer s.wg.Done()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := exchangeDNS(ctx, s.proxy, s.upstream, query)
	if err != nil {
		return
	}
	_, _ = s.pc.WriteTo(resp, addr)
}

func exchangeDNS(ctx context.Context, proxy Proxy, upstream string, query []byte) ([]byte, error) {
	conn, err := dialViaProxy(ctx, proxy, upstream)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	if len(query) > 65535 {
		return nil, fmt.Errorf("dns query too large")
	}

	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(query)))
	if _, err := conn.Write(hdr[:]); err != nil {
		return nil, err
	}
	if _, err := conn.Write(query); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return nil, err
	}

	n := int(binary.BigEndian.Uint16(hdr[:]))
	if n == 0 || n > 65535 {
		return nil, fmt.Errorf("invalid dns response length %d", n)
	}

	resp := make([]byte, n)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func dialViaProxy(ctx context.Context, proxy Proxy, target string) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", proxy.Address())
	if err != nil {
		return nil, err
	}

	deadline, ok := ctx.Deadline()
	if ok {
		_ = conn.SetDeadline(deadline)
	}

	switch proxy.Type {
	case "http":
		if err := httpConnect(conn, proxy, target); err != nil {
			_ = conn.Close()
			return nil, err
		}
	case "socks":
		if err := socks5Connect(conn, proxy, target); err != nil {
			_ = conn.Close()
			return nil, err
		}
	default:
		_ = conn.Close()
		return nil, fmt.Errorf("unsupported proxy type %q", proxy.Type)
	}

	return conn, nil
}

func httpConnect(conn net.Conn, proxy Proxy, target string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n", target, target)
	if proxy.Username != "" || proxy.Password != "" {
		token := base64.StdEncoding.EncodeToString([]byte(proxy.Username + ":" + proxy.Password))
		fmt.Fprintf(&b, "Proxy-Authorization: Basic %s\r\n", token)
	}
	b.WriteString("\r\n")

	if _, err := io.WriteString(conn, b.String()); err != nil {
		return err
	}

	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.Contains(status, " 200 ") {
		return fmt.Errorf("http proxy rejected CONNECT: %s", strings.TrimSpace(status))
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if line == "\r\n" {
			return nil
		}
	}
}

func socks5Connect(conn net.Conn, proxy Proxy, target string) error {
	_, err := socks5ClientRequest(conn, proxy, socksCommandConnect, target)
	return err
}
