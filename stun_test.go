package connectivity

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
	"time"
)

func startTestSTUN(t *testing.T) *STUN {
	t.Helper()
	l, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	alt := l.LocalAddr().(*net.UDPAddr).Port
	l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	s, err := StartSTUN(ctx, Config{StunListen: "127.0.0.1:0", StunPublicIP: "203.0.113.7", StunAltPort: alt})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); s.Close() })
	return s
}

func classic(txid byte, attrs ...byte) []byte {
	p := make([]byte, 20, 20+len(attrs))
	binary.BigEndian.PutUint16(p[0:], 1)
	binary.BigEndian.PutUint16(p[2:], uint16(len(attrs)))
	for i := 4; i < 20; i++ {
		p[i] = txid + byte(i)
	}
	return append(p, attrs...)
}

func exchange(t *testing.T, to *net.UDPAddr, pkt []byte, want bool) ([]byte, *net.UDPAddr) {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.WriteToUDP(pkt, to)
	wait := 150 * time.Millisecond
	if want {
		wait = 2 * time.Second
	}
	c.SetReadDeadline(time.Now().Add(wait))
	buf := make([]byte, 600)
	n, from, err := c.ReadFromUDP(buf)
	if err != nil {
		if want {
			t.Fatalf("no reply: %v", err)
		}
		return nil, nil
	}
	if !want {
		t.Fatalf("unexpected reply % x", buf[:n])
	}
	return append(buf[:n:n], byte(c.LocalAddr().(*net.UDPAddr).Port>>8), byte(c.LocalAddr().(*net.UDPAddr).Port)), from
}

func attrs(t *testing.T, b []byte) map[uint16][]byte {
	m := map[uint16][]byte{}
	for o := 20; o+4 <= len(b); {
		l := int(binary.BigEndian.Uint16(b[o+2:]))
		m[binary.BigEndian.Uint16(b[o:])] = b[o+4 : o+4+l]
		o += 4 + l
	}
	return m
}

func TestSTUNPlainRequest(t *testing.T) {
	s := startTestSTUN(t)
	a := s.Addrs()
	// Empty 0x0021 plus a USERNAME, as the PS3 firmware sends.
	req := classic(9, 0, 0x21, 0, 0, 0, 6, 0, 4, '0', '0', '0', '1')
	out, from := exchange(t, a[0], req, true)
	if from.Port != a[0].Port || binary.BigEndian.Uint16(out) != 0x0101 || string(out[4:20]) != string(req[4:20]) {
		t.Fatalf("bad reply header from %v", from)
	}
	m := attrs(t, out[:len(out)-2])
	cport := binary.BigEndian.Uint16(out[len(out)-2:])
	if binary.BigEndian.Uint16(m[1][2:]) != cport || netip.AddrFrom4([4]byte(m[1][4:8])).String() != "127.0.0.1" {
		t.Fatalf("bad MAPPED-ADDRESS % x", m[1])
	}
	if netip.AddrFrom4([4]byte(m[5][4:8])).String() != "203.0.113.7" || int(binary.BigEndian.Uint16(m[5][2:])) != a[1].Port {
		t.Fatalf("bad CHANGED-ADDRESS % x", m[5])
	}
	if int(binary.BigEndian.Uint16(m[4][2:])) != a[0].Port {
		t.Fatalf("bad SOURCE-ADDRESS % x", m[4])
	}
	// The alternate port answers a plain request from itself.
	if _, from := exchange(t, a[1], req, true); from.Port != a[1].Port {
		t.Fatalf("alt reply from %v", from)
	}
}

func TestSTUNChangePortAndChangeIP(t *testing.T) {
	s := startTestSTUN(t)
	a := s.Addrs()
	out, from := exchange(t, a[0], classic(1, 0, 3, 0, 4, 0, 0, 0, 2), true)
	m := attrs(t, out[:len(out)-2])
	if from.Port != a[1].Port || int(binary.BigEndian.Uint16(m[4][2:])) != a[1].Port || int(binary.BigEndian.Uint16(m[5][2:])) != a[0].Port {
		t.Fatalf("change-port should answer from the alternate port, got %v", from)
	}
	// change-IP cannot be honoured: answered like a plain request.
	if _, from := exchange(t, a[0], classic(2, 0, 3, 0, 4, 0, 0, 0, 4), true); from.Port != a[0].Port {
		t.Fatalf("change-ip answered from %v", from)
	}
}

func TestSTUNDropsMalformed(t *testing.T) {
	s := startTestSTUN(t)
	a := s.Addrs()
	cookie := classic(3)
	binary.BigEndian.PutUint32(cookie[4:], stunCookie)
	bad := map[string][]byte{
		"short":     {0, 1, 0, 0},
		"not-req":   append([]byte{0x01, 0x01}, classic(4)[2:]...),
		"cookie":    cookie,
		"len-large": append(classic(5), 0, 0, 0, 0)[:20],
		"truncated": classic(6, 0, 3, 0, 8, 0, 0, 0, 2),
		"trunc-hdr": classic(7, 0, 3),
	}
	binary.BigEndian.PutUint16(bad["len-large"][2:], 40)
	for name, p := range bad {
		t.Run(name, func(t *testing.T) { exchange(t, a[0], p, false) })
	}
	// still alive afterwards
	exchange(t, a[0], classic(8), true)
}

func TestSTUNRateLimit(t *testing.T) {
	s := &STUN{buckets: map[netip.Addr]*stunBucket{}}
	ip := netip.MustParseAddr("192.0.2.1")
	ok := 0
	for i := 0; i < 1000; i++ {
		if s.allow(ip) {
			ok++
		}
	}
	if ok < stunRateBurst || ok > stunRateBurst+5 {
		t.Fatalf("allowed %d", ok)
	}
}

func TestSTUNConfigValidation(t *testing.T) {
	for _, c := range []Config{
		{StunListen: "nonsense", StunPublicIP: "203.0.113.7", StunAltPort: 1},
		{StunListen: "127.0.0.1:0", StunPublicIP: "", StunAltPort: 1},
		{StunListen: "127.0.0.1:0", StunPublicIP: "203.0.113.7"},
	} {
		if s, err := StartSTUN(context.Background(), c); err == nil {
			s.Close()
			t.Fatalf("accepted %+v", c)
		}
	}
}
