package connectivity

import (
	"net"
	"net/netip"
	"time"
)

type relayLink struct {
	Relay
	owner     string
	conn      *net.UDPConn
	endpoints [2]netip.AddrPort
	credit    float64
	refilled  time.Time
	forwarded uint64
	dropped   uint64
}

func (s *Service) AllocateRelay(owner string, req RelayRequest) (Relay, error) {
	now := time.Now()
	expires, err := s.expiry(req.TTLSeconds, now)
	if err != nil || req.Participants[0] == req.Participants[1] {
		return Relay{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Relay{}, ErrUnavailable
	}
	s.expireLocked(now)
	first, err := s.participantLocked(owner, req.Participants[0], now)
	if err != nil {
		return Relay{}, err
	}
	second, err := s.participantLocked(owner, req.Participants[1], now)
	if err != nil {
		return Relay{}, err
	}
	if first.Key.Title != second.Key.Title || first.Key.Session != second.Key.Session || first.Key.Component != second.Key.Component {
		return Relay{}, ErrInvalid
	}
	expires = minTime(expires, minTime(first.ExpiresAt, second.ExpiresAt))
	count := 0
	used := map[int]bool{}
	for _, r := range s.relays {
		if r.owner == owner {
			count++
			if r.Participants == req.Participants {
				return r.Relay, nil
			}
		}
		used[int(r.conn.LocalAddr().(*net.UDPAddr).Port)] = true
	}
	if s.cfg.RelayPortMin == 0 {
		return Relay{}, ErrUnavailable
	}
	if count >= s.cfg.MaxRelaysPerBackend {
		return Relay{}, ErrCapacity
	}
	id, err := opaqueID()
	if err != nil {
		return Relay{}, err
	}
	bind := netip.MustParseAddr(s.cfg.RelayBindIP)
	public := netip.MustParseAddr(s.cfg.RelayPublicIP)
	for port := s.cfg.RelayPortMin; port <= s.cfg.RelayPortMax; port++ {
		if used[port] {
			continue
		}
		conn, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.AddrPortFrom(bind, uint16(port))))
		if err != nil {
			continue
		}
		r := &relayLink{Relay: Relay{ID: id, Endpoint: netip.AddrPortFrom(public, uint16(port)).String(), Participants: req.Participants, ExpiresAt: expires}, owner: owner, conn: conn, credit: float64(s.cfg.RelayBytesPerSecond), refilled: now}
		s.relays[id] = r
		s.workers.Add(1)
		go s.forward(r)
		return r.Relay, nil
	}
	return Relay{}, ErrCapacity
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// BindRelay requires observations toward THIS relay, registered by the trusted
// adapter after it verifies the native handshake. Other-socket STUN mappings do
// not qualify. Unknown sources are never learned from gameplay traffic.
func (s *Service) BindRelay(owner, id string, req BindRequest) (Relay, error) {
	var eps [2]netip.AddrPort
	for i, raw := range req.Endpoints {
		ap, err := endpoint(raw, s.cfg.AllowPrivateEndpoints)
		if err != nil {
			return Relay{}, err
		}
		eps[i] = ap
	}
	if eps[0] == eps[1] {
		return Relay{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	r, ok := s.relays[id]
	if !ok || r.owner != owner || !now.Before(r.ExpiresAt) {
		return Relay{}, ErrNotFound
	}
	public := netip.MustParseAddr(s.cfg.RelayPublicIP)
	for i, pid := range r.Participants {
		p, err := s.participantLocked(owner, pid, now)
		if err != nil {
			return Relay{}, err
		}
		if eps[i].Addr().Is4() != public.Is4() {
			return Relay{}, ErrInvalid
		}
		verified := false
		for _, c := range p.Candidates {
			if c.Kind == "observed" && c.Endpoint == eps[i].String() && c.Destination == r.Endpoint {
				verified = true
				break
			}
		}
		if !verified {
			return Relay{}, ErrInvalid
		}
	}
	r.endpoints = eps
	r.Bound = true
	return r.Relay, nil
}

func (s *Service) ReleaseRelay(owner, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.relays[id]
	if !ok || r.owner != owner || !time.Now().Before(r.ExpiresAt) {
		return ErrNotFound
	}
	r.conn.Close()
	delete(s.relays, id)
	return nil
}

func (s *Service) forward(r *relayLink) {
	defer s.workers.Done()
	buf := make([]byte, 65535)
	for {
		n, from, err := r.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}
		from = netip.AddrPortFrom(from.Addr().Unmap(), from.Port())
		s.mu.Lock()
		now := time.Now()
		var to netip.AddrPort
		if s.relays[r.ID] == r && r.Bound && now.Before(r.ExpiresAt) {
			if from == r.endpoints[0] {
				to = r.endpoints[1]
			} else if from == r.endpoints[1] {
				to = r.endpoints[0]
			}
		}
		if to.IsValid() {
			rate := float64(s.cfg.RelayBytesPerSecond)
			r.credit = min(rate, r.credit+now.Sub(r.refilled).Seconds()*rate)
			r.refilled = now
			if r.credit >= float64(n) {
				r.credit -= float64(n)
			} else {
				to = netip.AddrPort{}
			}
		}
		if to.IsValid() {
			// Keep revocation/binding serialized through the write: no packet is
			// emitted using a stale endpoint after Release or Bind returns.
			r.conn.SetWriteDeadline(now.Add(100 * time.Millisecond))
			if _, err := r.conn.WriteToUDPAddrPort(buf[:n], to); err == nil {
				r.forwarded += uint64(n)
			} else {
				r.dropped++
			}
		} else {
			r.dropped++
		}
		s.mu.Unlock()
	}
}
