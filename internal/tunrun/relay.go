package tunrun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

type Relay struct {
	listener net.Listener
	listenIP string
	proxy    Proxy
	target   string
	wg       sync.WaitGroup
}

func StartRelay(listenHost string, proxy Proxy) (*Relay, int, error) {
	addr := net.JoinHostPort(listenHost, "0")
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, 0, err
	}

	_, portText, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		_ = ln.Close()
		return nil, 0, err
	}
	port, err := net.LookupPort("tcp", portText)
	if err != nil {
		_ = ln.Close()
		return nil, 0, err
	}

	r := &Relay{
		listener: ln,
		listenIP: listenHost,
		proxy:    proxy,
		target:   proxy.Address(),
	}
	r.wg.Add(1)
	go r.accept()
	return r, port, nil
}

func (r *Relay) Close(ctx context.Context) error {
	_ = r.listener.Close()

	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (r *Relay) accept() {
	defer r.wg.Done()
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}

		r.wg.Add(1)
		go r.handle(conn)
	}
}

func (r *Relay) handle(in net.Conn) {
	defer r.wg.Done()
	defer in.Close()

	if r.proxy.Type == "socks" {
		r.handleSOCKS(in)
		return
	}
	r.handleRawTCP(in)
}

func (r *Relay) handleRawTCP(in net.Conn) {
	out, err := net.DialTimeout("tcp", r.target, 10*time.Second)
	if err != nil {
		return
	}
	defer out.Close()

	var copyWG sync.WaitGroup
	copyWG.Add(2)
	go proxyCopy(&copyWG, out, in)
	go proxyCopy(&copyWG, in, out)
	copyWG.Wait()
}

func (r *Relay) handleSOCKS(in net.Conn) {
	_ = in.SetDeadline(time.Now().Add(10 * time.Second))
	if err := acceptSOCKSClient(in); err != nil {
		return
	}

	req, err := readSOCKSRequest(in)
	if err != nil {
		return
	}
	_ = in.SetDeadline(time.Time{})

	switch req.command {
	case socksCommandConnect:
		r.handleSOCKSConnect(in, req.addr.String())
	case socksCommandUDPAssociate:
		r.handleSOCKSUDPAssociate(in)
	default:
		_ = writeSOCKSReply(in, socksReplyCommandNotSupported, nil)
	}
}

func (r *Relay) handleSOCKSConnect(in net.Conn, target string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	out, err := dialViaProxy(ctx, r.proxy, target)
	cancel()
	if err != nil {
		_ = writeSOCKSReply(in, socksReplyGeneralFailure, nil)
		return
	}
	defer out.Close()
	_ = out.SetDeadline(time.Time{})

	if err := writeSOCKSReply(in, socksReplySucceeded, nil); err != nil {
		return
	}

	var copyWG sync.WaitGroup
	copyWG.Add(2)
	go proxyCopy(&copyWG, out, in)
	go proxyCopy(&copyWG, in, out)
	copyWG.Wait()
}

func (r *Relay) handleSOCKSUDPAssociate(in net.Conn) {
	assoc, bindAddr, err := startUDPAssociation(r.listenIP, r.proxy)
	if err != nil {
		_ = writeSOCKSReply(in, socksReplyGeneralFailure, nil)
		return
	}
	defer assoc.Close()

	if err := writeSOCKSReply(in, socksReplySucceeded, bindAddr); err != nil {
		return
	}

	_, _ = io.Copy(io.Discard, in)
}

func proxyCopy(wg *sync.WaitGroup, dst net.Conn, src net.Conn) {
	defer wg.Done()
	_, _ = io.Copy(dst, src)

	if tcp, ok := dst.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
		return
	}
	_ = dst.SetDeadline(time.Now())
}

type udpAssociation struct {
	clientPC   net.PacketConn
	upstreamPC net.PacketConn
	upstream   net.Addr
	control    net.Conn

	closeOnce sync.Once
	wg        sync.WaitGroup

	mu         sync.RWMutex
	clientAddr net.Addr
}

func startUDPAssociation(listenHost string, proxy Proxy) (*udpAssociation, socksAddr, error) {
	control, upstream, err := dialSOCKSUDPAssociate(proxy)
	if err != nil {
		return nil, nil, err
	}

	clientPC, err := net.ListenPacket("udp", net.JoinHostPort(listenHost, "0"))
	if err != nil {
		_ = control.Close()
		return nil, nil, err
	}

	upstreamPC, err := net.ListenPacket("udp", "")
	if err != nil {
		_ = clientPC.Close()
		_ = control.Close()
		return nil, nil, err
	}

	_, portText, err := net.SplitHostPort(clientPC.LocalAddr().String())
	if err != nil {
		_ = upstreamPC.Close()
		_ = clientPC.Close()
		_ = control.Close()
		return nil, nil, err
	}
	port, err := net.LookupPort("udp", portText)
	if err != nil {
		_ = upstreamPC.Close()
		_ = clientPC.Close()
		_ = control.Close()
		return nil, nil, err
	}
	bindAddr, err := socksAddrFromHostPort(listenHost, port)
	if err != nil {
		_ = upstreamPC.Close()
		_ = clientPC.Close()
		_ = control.Close()
		return nil, nil, err
	}

	a := &udpAssociation{
		clientPC:   clientPC,
		upstreamPC: upstreamPC,
		upstream:   upstream,
		control:    control,
	}
	a.wg.Add(2)
	go a.copyClientToUpstream()
	go a.copyUpstreamToClient()
	return a, bindAddr, nil
}

func dialSOCKSUDPAssociate(proxy Proxy) (net.Conn, net.Addr, error) {
	conn, err := net.DialTimeout("tcp", proxy.Address(), 10*time.Second)
	if err != nil {
		return nil, nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	bindAddr, err := socks5ClientRequest(conn, proxy, socksCommandUDPAssociate, "0.0.0.0:0")
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	_ = conn.SetDeadline(time.Time{})

	upstream, err := resolveSOCKSUDPBind(bindAddr, proxy.Host)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return conn, upstream, nil
}

func resolveSOCKSUDPBind(addr socksAddr, fallbackHost string) (net.Addr, error) {
	host, port, err := addr.hostPort()
	if err != nil {
		return nil, err
	}

	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = fallbackHost
	}
	return net.ResolveUDPAddr("udp", net.JoinHostPort(host, fmt.Sprint(port)))
}

func (a *udpAssociation) Close() error {
	a.closeOnce.Do(func() {
		_ = a.clientPC.Close()
		_ = a.upstreamPC.Close()
		_ = a.control.Close()
	})
	a.wg.Wait()
	return nil
}

func (a *udpAssociation) copyClientToUpstream() {
	defer a.wg.Done()
	buf := make([]byte, 65535)
	for {
		n, addr, err := a.clientPC.ReadFrom(buf)
		if err != nil {
			return
		}

		a.mu.Lock()
		a.clientAddr = addr
		a.mu.Unlock()

		_, _ = a.upstreamPC.WriteTo(buf[:n], a.upstream)
	}
}

func (a *udpAssociation) copyUpstreamToClient() {
	defer a.wg.Done()
	buf := make([]byte, 65535)
	for {
		n, _, err := a.upstreamPC.ReadFrom(buf)
		if err != nil {
			return
		}

		a.mu.RLock()
		clientAddr := a.clientAddr
		a.mu.RUnlock()
		if clientAddr == nil {
			continue
		}
		_, _ = a.clientPC.WriteTo(buf[:n], clientAddr)
	}
}
