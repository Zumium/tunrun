package tunrun

import (
	"bytes"
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSOCKSRelayConnect(t *testing.T) {
	upstream := startFakeSOCKSUpstream(t)
	proxy, err := ParseProxy("socks5://" + upstream.addr)
	if err != nil {
		t.Fatal(err)
	}

	relay, port, err := StartRelay("127.0.0.1", proxy)
	if err != nil {
		skipSocketNotPermitted(t, err)
		t.Fatal(err)
	}
	defer closeRelay(t, relay)

	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := socks5ClientRequest(conn, Proxy{}, socksCommandConnect, "example.com:443"); err != nil {
		t.Fatal(err)
	}

	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}

	got := make([]byte, 5)
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q want hello", got)
	}
}

func TestSOCKSRelayUDPAssociate(t *testing.T) {
	upstream := startFakeSOCKSUpstream(t)
	proxy, err := ParseProxy("socks5://" + upstream.addr)
	if err != nil {
		t.Fatal(err)
	}

	relay, port, err := StartRelay("127.0.0.1", proxy)
	if err != nil {
		skipSocketNotPermitted(t, err)
		t.Fatal(err)
	}
	defer closeRelay(t, relay)

	control, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()

	bindAddr, err := socks5ClientRequest(control, Proxy{}, socksCommandUDPAssociate, "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	udpTarget, err := net.ResolveUDPAddr("udp", bindAddr.String())
	if err != nil {
		t.Fatal(err)
	}

	clientUDP, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		skipSocketNotPermitted(t, err)
		t.Fatal(err)
	}
	defer clientUDP.Close()

	dst, err := socksAddrFromHostPort("8.8.8.8", 53)
	if err != nil {
		t.Fatal(err)
	}
	packet := append([]byte{0x00, 0x00, 0x00}, dst...)
	packet = append(packet, []byte("ping")...)

	if _, err := clientUDP.WriteTo(packet, udpTarget); err != nil {
		t.Fatal(err)
	}

	if err := clientUDP.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024)
	n, _, err := clientUDP.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf[:n], packet) {
		t.Fatalf("got %x want %x", buf[:n], packet)
	}
}

type fakeSOCKSUpstream struct {
	addr string
	ln   net.Listener
	wg   sync.WaitGroup
}

func startFakeSOCKSUpstream(t *testing.T) *fakeSOCKSUpstream {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		skipSocketNotPermitted(t, err)
		t.Fatal(err)
	}

	s := &fakeSOCKSUpstream{
		addr: ln.Addr().String(),
		ln:   ln,
	}
	s.wg.Add(1)
	go s.accept()

	t.Cleanup(func() {
		_ = ln.Close()
		s.wg.Wait()
	})
	return s
}

func (s *fakeSOCKSUpstream) accept() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}

		s.wg.Add(1)
		go s.handle(conn)
	}
}

func (s *fakeSOCKSUpstream) handle(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	if err := acceptSOCKSClient(conn); err != nil {
		return
	}
	req, err := readSOCKSRequest(conn)
	if err != nil {
		return
	}

	switch req.command {
	case socksCommandConnect:
		if err := writeSOCKSReply(conn, socksReplySucceeded, nil); err != nil {
			return
		}
		_, _ = io.Copy(conn, conn)
	case socksCommandUDPAssociate:
		s.handleUDPAssociate(conn)
	default:
		_ = writeSOCKSReply(conn, socksReplyCommandNotSupported, nil)
	}
}

func (s *fakeSOCKSUpstream) handleUDPAssociate(conn net.Conn) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		_ = writeSOCKSReply(conn, socksReplyGeneralFailure, nil)
		return
	}
	defer pc.Close()

	host, portText, err := net.SplitHostPort(pc.LocalAddr().String())
	if err != nil {
		_ = writeSOCKSReply(conn, socksReplyGeneralFailure, nil)
		return
	}
	port, err := net.LookupPort("udp", portText)
	if err != nil {
		_ = writeSOCKSReply(conn, socksReplyGeneralFailure, nil)
		return
	}
	addr, err := socksAddrFromHostPort(host, port)
	if err != nil {
		_ = writeSOCKSReply(conn, socksReplyGeneralFailure, nil)
		return
	}
	if err := writeSOCKSReply(conn, socksReplySucceeded, addr); err != nil {
		return
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 65535)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo(buf[:n], addr)
		}
	}()

	_, _ = io.Copy(io.Discard, conn)
	_ = pc.Close()
	<-done
}

func closeRelay(t *testing.T, relay *Relay) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := relay.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func skipSocketNotPermitted(t *testing.T, err error) {
	t.Helper()
	if strings.Contains(err.Error(), "operation not permitted") {
		t.Skipf("local socket not permitted in this sandbox: %v", err)
	}
}
