package connectivity

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalid     = errors.New("invalid request")
	ErrNotFound    = errors.New("lease not found")
	ErrCapacity    = errors.New("capacity exhausted")
	ErrUnavailable = errors.New("resource unavailable")
)

type entry struct {
	owner string
	Participant
}

type Service struct {
	mu           sync.Mutex
	cfg          Config
	participants map[string]entry
	keys         map[scopedKey]string
	relays       map[string]*relayLink
	reports      map[reportKey]uint64
	closed       bool
	done         chan struct{}
	workers      sync.WaitGroup
}

type scopedKey struct {
	Owner string
	ParticipantKey
}

type reportKey struct{ owner, stage, outcome string }

func New(cfg Config) (*Service, error) {
	if len(cfg.BackendTokens) == 0 {
		return nil, fmt.Errorf("backend_tokens required")
	}
	tokens := map[string]string{}
	for owner, token := range cfg.BackendTokens {
		if !validLabel(owner) || len(token) < 32 {
			return nil, fmt.Errorf("backend namespaces require distinct tokens of at least 32 bytes")
		}
		if _, exists := tokens[token]; exists {
			return nil, fmt.Errorf("duplicate backend token")
		}
		tokens[token] = owner
	}
	if cfg.MaxTTLSeconds == 0 {
		cfg.MaxTTLSeconds = 3600
	}
	if cfg.MaxParticipantsPerBackend == 0 {
		cfg.MaxParticipantsPerBackend = 1024
	}
	if cfg.MaxRelaysPerBackend == 0 {
		cfg.MaxRelaysPerBackend = 128
	}
	if cfg.RelayBytesPerSecond == 0 {
		cfg.RelayBytesPerSecond = 1024 * 1024
	}
	if cfg.MaxTTLSeconds < 1 || cfg.MaxTTLSeconds > 86400 || cfg.MaxParticipantsPerBackend < 1 || cfg.MaxRelaysPerBackend < 1 || cfg.RelayBytesPerSecond < 65507 {
		return nil, fmt.Errorf("invalid limits")
	}
	if cfg.RelayPortMin != 0 || cfg.RelayPortMax != 0 {
		bind, e1 := netip.ParseAddr(cfg.RelayBindIP)
		public, e2 := netip.ParseAddr(cfg.RelayPublicIP)
		if e1 != nil || e2 != nil || bind.Is4() != public.Is4() || cfg.RelayPortMin < 1024 || cfg.RelayPortMax < cfg.RelayPortMin || cfg.RelayPortMax > 65535 || !usableIP(public, cfg.AllowPrivateEndpoints) {
			return nil, fmt.Errorf("invalid relay configuration")
		}
	}
	ids := map[string]bool{}
	homes := 0
	for _, r := range cfg.Resources {
		if !validLabel(r.ID) || ids[r.ID] || !validServer(r.STUN) || !validServer(r.TURN) || len(r.TURNSecret) < 32 {
			return nil, fmt.Errorf("invalid or duplicate TURN resource")
		}
		ids[r.ID] = true
		if r.Home {
			homes++
		}
	}
	if homes > 1 {
		return nil, fmt.Errorf("only one home resource allowed")
	}
	// Copy credentials and resource configuration so callers cannot mutate live policy.
	cfg.BackendTokens = map[string]string{}
	for token, owner := range tokens {
		cfg.BackendTokens[owner] = token
	}
	cfg.Resources = append([]Resource(nil), cfg.Resources...)
	s := &Service{cfg: cfg, participants: map[string]entry{}, keys: map[scopedKey]string{}, relays: map[string]*relayLink{}, reports: map[reportKey]uint64{}, done: make(chan struct{})}
	s.workers.Add(1)
	go s.sweep()
	return s, nil
}

func validLabel(v string) bool {
	if len(v) == 0 || len(v) > 128 {
		return false
	}
	for _, c := range v {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

func validServer(v string) bool {
	host, port, err := splitServer(v)
	return err == nil && host != "" && port > 0
}

func usableIP(ip netip.Addr, private bool) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if ip.Is4() && ip.As4() == [4]byte{255, 255, 255, 255} {
		return false
	}
	return private || (ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !netip.MustParsePrefix("100.64.0.0/10").Contains(ip))
}

func endpoint(v string, private bool) (netip.AddrPort, error) {
	ap, err := netip.ParseAddrPort(v)
	if err != nil || ap.Port() == 0 || ap.Addr().Zone() != "" || !usableIP(ap.Addr(), private) {
		return netip.AddrPort{}, ErrInvalid
	}
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()), nil
}

func opaqueID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *Service) authenticate(token string) string {
	for owner, secret := range s.cfg.BackendTokens {
		if subtle.ConstantTimeCompare([]byte(token), []byte(secret)) == 1 {
			return owner
		}
	}
	return ""
}

func (s *Service) expiry(ttl int, now time.Time) (time.Time, error) {
	if ttl < 1 || ttl > s.cfg.MaxTTLSeconds {
		return time.Time{}, ErrInvalid
	}
	return now.Add(time.Duration(ttl) * time.Second), nil
}

func (s *Service) Register(owner string, req RegisterRequest) (Participant, error) {
	if !validLabel(req.Key.Title) || !validLabel(req.Key.Session) || !validLabel(req.Key.Player) || !validLabel(req.Key.Generation) || !validLabel(req.Key.Component) || len(req.Candidates) > 16 {
		return Participant{}, ErrInvalid
	}
	now := time.Now()
	expires, err := s.expiry(req.TTLSeconds, now)
	if err != nil {
		return Participant{}, err
	}
	candidates := make([]Candidate, 0, len(req.Candidates))
	seen := map[Candidate]bool{}
	for _, c := range req.Candidates {
		if c.Kind != "lan" && c.Kind != "reported_public" && c.Kind != "observed" {
			return Participant{}, ErrInvalid
		}
		ap, err := endpoint(c.Endpoint, c.Kind == "lan" || s.cfg.AllowPrivateEndpoints)
		if err != nil {
			return Participant{}, err
		}
		c.Endpoint = ap.String()
		if c.Kind == "observed" {
			dest, err := endpoint(c.Destination, s.cfg.AllowPrivateEndpoints)
			if err != nil {
				return Participant{}, err
			}
			c.Destination = dest.String()
		} else if c.Destination != "" {
			return Participant{}, ErrInvalid
		}
		if !seen[c] {
			candidates = append(candidates, c)
			seen[c] = true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Participant{}, ErrUnavailable
	}
	if _, ok := s.cfg.BackendTokens[owner]; !ok {
		return Participant{}, ErrInvalid
	}
	s.expireLocked(now)
	key := scopedKey{owner, req.Key}
	id := s.keys[key]
	if id == "" {
		count := 0
		for _, p := range s.participants {
			if p.owner == owner {
				count++
			}
		}
		if count >= s.cfg.MaxParticipantsPerBackend {
			return Participant{}, ErrCapacity
		}
		id, err = opaqueID()
		if err != nil {
			return Participant{}, err
		}
	}
	p := Participant{ID: id, Key: req.Key, Candidates: candidates, ExpiresAt: expires}
	s.participants[id] = entry{owner, p}
	s.keys[key] = id
	// Shortening a participant lease also shortens existing relay authority.
	for _, r := range s.relays {
		if (r.Participants[0] == id || r.Participants[1] == id) && expires.Before(r.ExpiresAt) {
			r.ExpiresAt = expires
		}
		for i, pid := range r.Participants {
			if pid != id || !r.Bound {
				continue
			}
			found := false
			for _, candidate := range candidates {
				if candidate.Kind == "observed" && candidate.Endpoint == r.endpoints[i].String() && candidate.Destination == r.Endpoint {
					found = true
					break
				}
			}
			if !found {
				r.Bound = false
			}
		}
	}
	p.Candidates = append([]Candidate(nil), p.Candidates...)
	return p, nil
}

func (s *Service) participantLocked(owner, id string, now time.Time) (entry, error) {
	p, ok := s.participants[id]
	if !ok || p.owner != owner || !now.Before(p.ExpiresAt) {
		return entry{}, ErrNotFound
	}
	return p, nil
}

func (s *Service) Get(owner, id string) (Participant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.participantLocked(owner, id, time.Now())
	p.Candidates = append([]Candidate(nil), p.Candidates...)
	return p.Participant, err
}

func (s *Service) Release(owner, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.participantLocked(owner, id, time.Now()); err != nil {
		return err
	}
	s.releaseLocked(id)
	return nil
}

func (s *Service) releaseLocked(id string) {
	p := s.participants[id]
	delete(s.keys, scopedKey{p.owner, p.Key})
	delete(s.participants, id)
	for rid, r := range s.relays {
		if r.Participants[0] == id || r.Participants[1] == id {
			r.conn.Close()
			delete(s.relays, rid)
		}
	}
}

func (s *Service) ICE(owner string, req ICERequest) (ICEResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.participantLocked(owner, req.Participant, time.Now())
	if err != nil {
		return ICEResponse{}, err
	}
	var chosen *Resource
	for i := range s.cfg.Resources {
		r := &s.cfg.Resources[i]
		if !r.Healthy {
			continue
		}
		if r.Home {
			chosen = r
		}
	}
	for i := range s.cfg.Resources {
		r := &s.cfg.Resources[i]
		if r.Healthy && req.Country != "" && strings.EqualFold(r.Country, req.Country) {
			chosen = r
			break
		}
	}
	if chosen == nil {
		return ICEResponse{}, ErrUnavailable
	}
	// coturn REST credentials are per participant lease, not per public address.
	username := fmt.Sprintf("%d:%s", p.ExpiresAt.Unix(), p.ID)
	mac := hmac.New(sha1.New, []byte(chosen.TURNSecret))
	mac.Write([]byte(username))
	return ICEResponse{Resource: chosen.ID, STUN: chosen.STUN, TURN: chosen.TURN, Username: username, Password: base64.StdEncoding.EncodeToString(mac.Sum(nil)), ExpiresAt: p.ExpiresAt}, nil
}

func (s *Service) Report(owner string, req ReportRequest) error {
	stages := map[string]bool{"identity": true, "registration": true, "candidates": true, "traversal": true, "relay": true, "gameplay": true}
	if !stages[req.Stage] || (req.Outcome != "success" && req.Outcome != "failure" && req.Outcome != "timeout") {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.participantLocked(owner, req.Participant, time.Now()); err != nil {
		return err
	}
	s.reports[reportKey{owner, req.Stage, req.Outcome}]++
	return nil
}

func (s *Service) expireLocked(now time.Time) {
	for id, p := range s.participants {
		if !now.Before(p.ExpiresAt) {
			s.releaseLocked(id)
		}
	}
	for id, r := range s.relays {
		if !now.Before(r.ExpiresAt) {
			r.conn.Close()
			delete(s.relays, id)
		}
	}
}

func (s *Service) sweep() {
	defer s.workers.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case now := <-ticker.C:
			s.mu.Lock()
			s.expireLocked(now)
			s.mu.Unlock()
		}
	}
}

func (s *Service) Close() error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.done)
		for id, r := range s.relays {
			r.conn.Close()
			delete(s.relays, id)
		}
		s.participants = map[string]entry{}
		s.keys = map[scopedKey]string{}
	}
	s.mu.Unlock()
	s.workers.Wait()
	return nil
}
