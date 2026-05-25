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
	target   string
	wg       sync.WaitGroup
}

func StartRelay(listenHost string, targetHost string, targetPort int) (*Relay, int, error) {
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
		target:   net.JoinHostPort(targetHost, fmt.Sprint(targetPort)),
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

func proxyCopy(wg *sync.WaitGroup, dst net.Conn, src net.Conn) {
	defer wg.Done()
	_, _ = io.Copy(dst, src)

	if tcp, ok := dst.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
		return
	}
	_ = dst.SetDeadline(time.Now())
}
