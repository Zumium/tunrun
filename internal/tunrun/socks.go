package tunrun

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

const (
	socksVersion        = 0x05
	socksAuthVersion    = 0x01
	socksMethodNoAuth   = 0x00
	socksMethodUserPass = 0x02
	socksMethodNone     = 0xff

	socksCommandConnect      = 0x01
	socksCommandUDPAssociate = 0x03

	socksReplySucceeded           = 0x00
	socksReplyGeneralFailure      = 0x01
	socksReplyCommandNotSupported = 0x07

	socksAtypIPv4   = 0x01
	socksAtypDomain = 0x03
	socksAtypIPv6   = 0x04
)

type socksAddr []byte

type socksRequest struct {
	command byte
	addr    socksAddr
}

func acceptSOCKSClient(rw io.ReadWriter) error {
	var head [2]byte
	if _, err := io.ReadFull(rw, head[:]); err != nil {
		return err
	}
	if head[0] != socksVersion {
		return fmt.Errorf("unsupported SOCKS version %d", head[0])
	}

	methods := make([]byte, int(head[1]))
	if _, err := io.ReadFull(rw, methods); err != nil {
		return err
	}

	method := byte(socksMethodNone)
	for _, m := range methods {
		if m == socksMethodNoAuth {
			method = socksMethodNoAuth
			break
		}
	}
	if method == socksMethodNone {
		for _, m := range methods {
			if m == socksMethodUserPass {
				method = socksMethodUserPass
				break
			}
		}
	}

	if _, err := rw.Write([]byte{socksVersion, method}); err != nil {
		return err
	}
	if method == socksMethodNone {
		return fmt.Errorf("SOCKS client offered no supported auth methods")
	}
	if method == socksMethodUserPass {
		return acceptSOCKSUserPass(rw)
	}
	return nil
}

func acceptSOCKSUserPass(rw io.ReadWriter) error {
	var head [2]byte
	if _, err := io.ReadFull(rw, head[:]); err != nil {
		return err
	}
	if head[0] != socksAuthVersion {
		_, _ = rw.Write([]byte{socksAuthVersion, 0x01})
		return fmt.Errorf("unsupported SOCKS auth version %d", head[0])
	}

	username := make([]byte, int(head[1]))
	if _, err := io.ReadFull(rw, username); err != nil {
		return err
	}

	var plen [1]byte
	if _, err := io.ReadFull(rw, plen[:]); err != nil {
		return err
	}
	password := make([]byte, int(plen[0]))
	if _, err := io.ReadFull(rw, password); err != nil {
		return err
	}

	_, err := rw.Write([]byte{socksAuthVersion, 0x00})
	return err
}

func readSOCKSRequest(r io.Reader) (socksRequest, error) {
	var head [3]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return socksRequest{}, err
	}
	if head[0] != socksVersion {
		return socksRequest{}, fmt.Errorf("unsupported SOCKS version %d", head[0])
	}
	if head[2] != 0x00 {
		return socksRequest{}, fmt.Errorf("invalid SOCKS reserved byte %#x", head[2])
	}

	addr, err := readSOCKSAddr(r)
	if err != nil {
		return socksRequest{}, err
	}
	return socksRequest{command: head[1], addr: addr}, nil
}

func readSOCKSAddr(r io.Reader) (socksAddr, error) {
	var atyp [1]byte
	if _, err := io.ReadFull(r, atyp[:]); err != nil {
		return nil, err
	}

	var rest []byte
	switch atyp[0] {
	case socksAtypIPv4:
		rest = make([]byte, net.IPv4len+2)
	case socksAtypIPv6:
		rest = make([]byte, net.IPv6len+2)
	case socksAtypDomain:
		var l [1]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return nil, err
		}
		rest = make([]byte, 1+int(l[0])+2)
		rest[0] = l[0]
	default:
		return nil, fmt.Errorf("unsupported SOCKS address type %#x", atyp[0])
	}

	offset := 0
	if atyp[0] == socksAtypDomain {
		offset = 1
	}
	if _, err := io.ReadFull(r, rest[offset:]); err != nil {
		return nil, err
	}

	addr := make([]byte, 1+len(rest))
	addr[0] = atyp[0]
	copy(addr[1:], rest)
	return socksAddr(addr), nil
}

func writeSOCKSReply(w io.Writer, reply byte, addr socksAddr) error {
	if addr == nil {
		var err error
		addr, err = socksAddrFromHostPort("0.0.0.0", 0)
		if err != nil {
			return err
		}
	}

	msg := make([]byte, 0, 3+len(addr))
	msg = append(msg, socksVersion, reply, 0x00)
	msg = append(msg, addr...)
	_, err := w.Write(msg)
	return err
}

func socksAddrFromHostPort(host string, port int) (socksAddr, error) {
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("invalid SOCKS port %d", port)
	}

	var portBytes [2]byte
	binary.BigEndian.PutUint16(portBytes[:], uint16(port))

	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			addr := make([]byte, 1, 1+net.IPv4len+2)
			addr[0] = socksAtypIPv4
			addr = append(addr, v4...)
			addr = append(addr, portBytes[:]...)
			return socksAddr(addr), nil
		}
		if v6 := ip.To16(); v6 != nil {
			addr := make([]byte, 1, 1+net.IPv6len+2)
			addr[0] = socksAtypIPv6
			addr = append(addr, v6...)
			addr = append(addr, portBytes[:]...)
			return socksAddr(addr), nil
		}
	}

	if len(host) > 255 {
		return nil, fmt.Errorf("SOCKS host too long")
	}
	addr := make([]byte, 0, 1+1+len(host)+2)
	addr = append(addr, socksAtypDomain, byte(len(host)))
	addr = append(addr, host...)
	addr = append(addr, portBytes[:]...)
	return socksAddr(addr), nil
}

func (a socksAddr) hostPort() (string, int, error) {
	if len(a) < 1 {
		return "", 0, fmt.Errorf("empty SOCKS address")
	}

	switch a[0] {
	case socksAtypIPv4:
		if len(a) != 1+net.IPv4len+2 {
			return "", 0, fmt.Errorf("invalid IPv4 SOCKS address length %d", len(a))
		}
		host := net.IP(a[1 : 1+net.IPv4len]).String()
		port := int(binary.BigEndian.Uint16(a[1+net.IPv4len:]))
		return host, port, nil
	case socksAtypIPv6:
		if len(a) != 1+net.IPv6len+2 {
			return "", 0, fmt.Errorf("invalid IPv6 SOCKS address length %d", len(a))
		}
		host := net.IP(a[1 : 1+net.IPv6len]).String()
		port := int(binary.BigEndian.Uint16(a[1+net.IPv6len:]))
		return host, port, nil
	case socksAtypDomain:
		if len(a) < 1+1+2 {
			return "", 0, fmt.Errorf("invalid domain SOCKS address length %d", len(a))
		}
		l := int(a[1])
		if len(a) != 1+1+l+2 {
			return "", 0, fmt.Errorf("invalid domain SOCKS address length %d", len(a))
		}
		host := string(a[2 : 2+l])
		port := int(binary.BigEndian.Uint16(a[2+l:]))
		return host, port, nil
	default:
		return "", 0, fmt.Errorf("unsupported SOCKS address type %#x", a[0])
	}
}

func (a socksAddr) String() string {
	host, port, err := a.hostPort()
	if err != nil {
		return ""
	}
	return net.JoinHostPort(host, fmt.Sprint(port))
}

func socks5ClientRequest(rw io.ReadWriter, proxy Proxy, command byte, target string) (socksAddr, error) {
	if proxy.Username != "" || proxy.Password != "" {
		if _, err := rw.Write([]byte{socksVersion, 0x02, socksMethodNoAuth, socksMethodUserPass}); err != nil {
			return nil, err
		}
	} else if _, err := rw.Write([]byte{socksVersion, 0x01, socksMethodNoAuth}); err != nil {
		return nil, err
	}

	var method [2]byte
	if _, err := io.ReadFull(rw, method[:]); err != nil {
		return nil, err
	}
	if method[0] != socksVersion {
		return nil, fmt.Errorf("invalid SOCKS version %d", method[0])
	}
	if method[1] == socksMethodNone {
		return nil, fmt.Errorf("SOCKS proxy has no acceptable auth method")
	}
	if method[1] == socksMethodUserPass {
		if err := socks5ClientUserPass(rw, proxy.Username, proxy.Password); err != nil {
			return nil, err
		}
	} else if method[1] != socksMethodNoAuth {
		return nil, fmt.Errorf("unsupported SOCKS auth method %#x", method[1])
	}

	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	port, err := net.LookupPort("tcp", portText)
	if err != nil {
		return nil, err
	}
	addr, err := socksAddrFromHostPort(host, port)
	if err != nil {
		return nil, err
	}

	req := make([]byte, 0, 3+len(addr))
	req = append(req, socksVersion, command, 0x00)
	req = append(req, addr...)
	if _, err := rw.Write(req); err != nil {
		return nil, err
	}

	var reply [3]byte
	if _, err := io.ReadFull(rw, reply[:]); err != nil {
		return nil, err
	}
	if reply[0] != socksVersion {
		return nil, fmt.Errorf("invalid SOCKS version %d", reply[0])
	}

	bindAddr, err := readSOCKSAddr(rw)
	if err != nil {
		return nil, err
	}
	if reply[1] != socksReplySucceeded {
		return nil, fmt.Errorf("SOCKS request failed with code %#x", reply[1])
	}
	return bindAddr, nil
}

func socks5ClientUserPass(rw io.ReadWriter, username, password string) error {
	if len(username) > 255 || len(password) > 255 {
		return fmt.Errorf("SOCKS username/password too long")
	}

	req := make([]byte, 0, 3+len(username)+len(password))
	req = append(req, socksAuthVersion, byte(len(username)))
	req = append(req, username...)
	req = append(req, byte(len(password)))
	req = append(req, password...)
	if _, err := rw.Write(req); err != nil {
		return err
	}

	var resp [2]byte
	if _, err := io.ReadFull(rw, resp[:]); err != nil {
		return err
	}
	if resp[0] != socksAuthVersion {
		return fmt.Errorf("invalid SOCKS auth version %d", resp[0])
	}
	if resp[1] != 0x00 {
		return fmt.Errorf("SOCKS username/password authentication failed")
	}
	return nil
}
