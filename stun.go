package connectivity

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"
)

// Classic (RFC 3489, cookie-less) STUN responder for console NAT checks, for
// example the PS3 SceNetStun client. RFC 5389 traffic (magic cookie) is not
// answered here; the host keeps routing it to coturn.

const (
	stunHeader       = 20
	stunMaxPacket    = 548 // larger requests are never legitimate; read buffer is fixed
	stunCookie       = 0x2112A442
	stunBindingReq   = 0x0001
	stunBindingResp  = 0x0101
	stunMapped       = 0x0001
	stunChangeReq    = 0x0003
	stunSource       = 0x0004
	stunChanged      = 0x0005
	stunChangePort   = 0x02
	stunRatePerSec   = 20
	stunRateBurst    = 40
	stunMaxSources   = 4096
	stunWriteTimeout = time.Second
)

// STUN serves classic binding requests on a primary and an alternate UDP port.
type STUN struct {
	conns      [2]*net.UDPConn
	publicIP   netip.Addr
	pubPort    [2]uint16
	mu         sync.Mutex
	buckets    map[netip.Addr]*stunBucket
	wg         sync.WaitGroup
	closeOnce  sync.Once
	cancelStop func()
}

type stunBucket struct {
	tokens float64
	last   time.Time
}

// StartSTUN binds the sockets and serves until ctx is done or Close is called.
// cfg.StunListen must be set. The alternate socket binds the same host as
// StunListen on StunAltPort; replies advertise StunPublicIP and, for the primary
// socket, StunPublicPort (default: the bound port, which differs when the host
// redirects the public port to a local one).
func StartSTUN(ctx context.Context, cfg Config) (*STUN, error) {
	host, port, err := net.SplitHostPort(cfg.StunListen)
	if err != nil {
		return nil, fmt.Errorf("invalid stun_listen")
	}
	ip, err := netip.ParseAddr(cfg.StunPublicIP)
	if err != nil || !ip.Is4() || ip.IsUnspecified() {
		return nil, fmt.Errorf("stun_public_ip must be an IPv4 address")
	}
	if cfg.StunAltPort < 1 || cfg.StunAltPort > 65535 || cfg.StunPublicPort < 0 || cfg.StunPublicPort > 65535 {
		return nil, fmt.Errorf("invalid stun ports")
	}
	s := &STUN{publicIP: ip, buckets: map[netip.Addr]*stunBucket{}}
	addrs := [2]string{net.JoinHostPort(host, port), net.JoinHostPort(host, fmt.Sprint(cfg.StunAltPort))}
	for i, a := range addrs {
		ua, err := net.ResolveUDPAddr("udp4", a)
		if err != nil {
			s.closeConns()
			return nil, fmt.Errorf("invalid stun address")
		}
		c, err := net.ListenUDP("udp4", ua)
		if err != nil {
			s.closeConns()
			return nil, fmt.Errorf("cannot bind STUN socket")
		}
		s.conns[i] = c
		s.pubPort[i] = uint16(c.LocalAddr().(*net.UDPAddr).Port)
	}
	if cfg.StunPublicPort != 0 {
		s.pubPort[0] = uint16(cfg.StunPublicPort)
	}
	ctx, s.cancelStop = context.WithCancel(ctx)
	for i := range s.conns {
		s.wg.Add(1)
		go s.serve(i)
	}
	go func() { <-ctx.Done(); s.Close() }()
	return s, nil
}

// Addrs returns the bound local addresses (primary, alternate).
func (s *STUN) Addrs() [2]*net.UDPAddr {
	return [2]*net.UDPAddr{s.conns[0].LocalAddr().(*net.UDPAddr), s.conns[1].LocalAddr().(*net.UDPAddr)}
}

func (s *STUN) closeConns() {
	for _, c := range s.conns {
		if c != nil {
			c.Close()
		}
	}
}

// Close stops both sockets and waits for the readers.
func (s *STUN) Close() {
	s.closeOnce.Do(func() {
		s.cancelStop()
		s.closeConns()
	})
	s.wg.Wait()
}

func (s *STUN) serve(i int) {
	defer s.wg.Done()
	buf := make([]byte, stunMaxPacket+1)
	for {
		n, from, err := s.conns[i].ReadFromUDPAddrPort(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		if !s.allow(from.Addr()) {
			continue
		}
		out, via := s.reply(i, buf[:n], from)
		if out == nil {
			continue
		}
		s.conns[via].SetWriteDeadline(time.Now().Add(stunWriteTimeout))
		s.conns[via].WriteToUDPAddrPort(out, from)
	}
}

// reply builds the response to d received on socket i and picks the socket to
// answer from. Anything that is not a well-formed classic Binding Request
// yields nil. CHANGE-REQUEST change-IP cannot be honoured with one public IP
// and is answered like a plain request; change-port answers from the other socket.
func (s *STUN) reply(i int, d []byte, from netip.AddrPort) ([]byte, int) {
	if len(d) < stunHeader || len(d) > stunMaxPacket || !from.Addr().Is4() {
		return nil, 0
	}
	if binary.BigEndian.Uint16(d[0:2]) != stunBindingReq || binary.BigEndian.Uint32(d[4:8]) == stunCookie {
		return nil, 0
	}
	n := int(binary.BigEndian.Uint16(d[2:4]))
	if n > len(d)-stunHeader {
		return nil, 0
	}
	via := i
	for o := stunHeader; o < stunHeader+n; {
		if o+4 > stunHeader+n {
			return nil, 0
		}
		t, l := binary.BigEndian.Uint16(d[o:]), int(binary.BigEndian.Uint16(d[o+2:]))
		if o+4+l > stunHeader+n {
			return nil, 0 // truncated attribute
		}
		if t == stunChangeReq && l == 4 && d[o+4+3]&stunChangePort != 0 {
			via = 1 - i
		}
		o += 4 + (l+3)/4*4
	}
	body := make([]byte, 0, 3*12)
	body = stunAddr(body, stunMapped, from.Addr(), from.Port())
	body = stunAddr(body, stunSource, s.publicIP, s.pubPort[via])
	body = stunAddr(body, stunChanged, s.publicIP, s.pubPort[1-via])
	out := make([]byte, stunHeader, stunHeader+len(body))
	binary.BigEndian.PutUint16(out[0:], stunBindingResp)
	binary.BigEndian.PutUint16(out[2:], uint16(len(body)))
	copy(out[4:stunHeader], d[4:stunHeader])
	return append(out, body...), via
}

func stunAddr(b []byte, t uint16, ip netip.Addr, port uint16) []byte {
	a := ip.As4()
	b = binary.BigEndian.AppendUint16(b, t)
	b = binary.BigEndian.AppendUint16(b, 8)
	b = append(b, 0, 1)
	b = binary.BigEndian.AppendUint16(b, port)
	return append(b, a[:]...)
}

// allow is a per-source token bucket; the table is bounded and reset when full.
func (s *STUN) allow(ip netip.Addr) bool {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.buckets[ip]
	if b == nil {
		if len(s.buckets) >= stunMaxSources {
			s.buckets = map[netip.Addr]*stunBucket{}
		}
		b = &stunBucket{tokens: stunRateBurst, last: now}
		s.buckets[ip] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * stunRatePerSec
	if b.tokens > stunRateBurst {
		b.tokens = stunRateBurst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
