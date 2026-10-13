package connectivity

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{BackendTokens: map[string]string{"nex": strings.Repeat("a", 32), "demonware": strings.Repeat("b", 32)}, AllowPrivateEndpoints: true, MaxTTLSeconds: 60, Resources: []Resource{
		{ID: "home", Country: "IE", STUN: "stun.example:3478", TURN: "turn.example:3478", TURNSecret: strings.Repeat("c", 32), Healthy: true, Home: true},
		{ID: "au", Country: "AU", STUN: "au.example:3478", TURN: "au.example:3478", TURNSecret: strings.Repeat("d", 32), Healthy: true},
	}}
}

func newService(t *testing.T, cfg Config) *Service {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func registration(player string) RegisterRequest {
	return RegisterRequest{Key: ParticipantKey{Title: "game", Session: "room", Player: player, Generation: "login-1", Component: "game-udp"}, TTLSeconds: 30, Candidates: []Candidate{{Endpoint: "192.168.1.10:3074", Kind: "lan"}, {Endpoint: "203.0.113.8:40001", Kind: "reported_public"}}}
}

func register(t *testing.T, s *Service, owner string, req RegisterRequest) Participant {
	t.Helper()
	p, err := s.Register(owner, req)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHouseholdParticipantsRemainIndependent(t *testing.T) {
	s := newService(t, testConfig())
	ids := map[string]bool{}
	for i := 0; i < 3; i++ {
		req := registration(fmt.Sprint(i))
		req.Candidates[0].Endpoint = fmt.Sprintf("192.168.1.%d:3074", 10+i)
		req.Candidates[1].Endpoint = fmt.Sprintf("203.0.113.8:%d", 40001+i)
		p := register(t, s, "nex", req)
		if ids[p.ID] {
			t.Fatal("participants collapsed")
		}
		ids[p.ID] = true
		got, err := s.Get("nex", p.ID)
		if err != nil || len(got.Candidates) != 2 || got.Candidates[0].Endpoint != req.Candidates[0].Endpoint {
			t.Fatal("lost independent candidates")
		}
		if again := register(t, s, "nex", req); again.ID != p.ID {
			t.Fatal("registration retry not idempotent")
		}
	}
}

func TestNamespaceGenerationAndSessionIsolation(t *testing.T) {
	s := newService(t, testConfig())
	req := registration("one")
	p := register(t, s, "nex", req)
	other := register(t, s, "demonware", req)
	if other.ID == p.ID {
		t.Fatal("namespaces collapsed")
	}
	if _, err := s.Get("demonware", p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign participant visible")
	}
	if err := s.Release("demonware", p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign participant released")
	}
	req.Key.Generation = "login-2"
	reconnected := register(t, s, "nex", req)
	if reconnected.ID == p.ID {
		t.Fatal("generation reused")
	}
	req.Key.Session = "another-room"
	foreignRoom := register(t, s, "nex", req)
	if _, err := s.AllocateRelay("nex", RelayRequest{Participants: [2]string{p.ID, foreignRoom.ID}, TTLSeconds: 10}); !errors.Is(err, ErrInvalid) {
		t.Fatal("cross-room relay allowed")
	}
	if _, err := s.AllocateRelay("nex", RelayRequest{Participants: [2]string{p.ID, other.ID}, TTLSeconds: 10}); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-backend relay allowed")
	}
}

func TestCandidateValidationAndQuota(t *testing.T) {
	cfg := testConfig()
	cfg.MaxParticipantsPerBackend = 1
	cfg.AllowPrivateEndpoints = false
	s := newService(t, cfg)
	for _, candidate := range []Candidate{
		{Endpoint: "203.0.113.1:0", Kind: "observed", Destination: "203.0.113.2:3478"},
		{Endpoint: "203.0.113.1:4000", Kind: "observed"},
		{Endpoint: "127.0.0.1:4000", Kind: "reported_public"},
		{Endpoint: "100.64.0.1:4000", Kind: "reported_public"},
		{Endpoint: "255.255.255.255:4000", Kind: "lan"},
		{Endpoint: "[ff02::1]:4000", Kind: "lan"},
		{Endpoint: "203.0.113.1:4000", Kind: "guessed"},
	} {
		req := registration("bad")
		req.Candidates = []Candidate{candidate}
		if _, err := s.Register("nex", req); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted invalid candidate %v", candidate)
		}
	}
	p := register(t, s, "nex", registration("one"))
	if _, err := s.Register("nex", registration("two")); !errors.Is(err, ErrCapacity) {
		t.Fatal("quota not enforced")
	}
	if err := s.Release("nex", p.ID); err != nil {
		t.Fatal(err)
	}
	register(t, s, "nex", registration("two"))
}

func TestICECredentialsAreLeaseScopedAndRegional(t *testing.T) {
	s := newService(t, testConfig())
	a := register(t, s, "nex", registration("one"))
	b := register(t, s, "nex", registration("two"))
	ice, err := s.ICE("nex", ICERequest{Participant: a.ID, Country: "au"})
	if err != nil || ice.Resource != "au" || !ice.ExpiresAt.Equal(a.ExpiresAt) {
		t.Fatalf("bad allocation: %v", err)
	}
	mac := hmac.New(sha1.New, []byte(strings.Repeat("d", 32)))
	mac.Write([]byte(ice.Username))
	if ice.Password != base64.StdEncoding.EncodeToString(mac.Sum(nil)) {
		t.Fatal("coturn credential incompatible")
	}
	second, err := s.ICE("nex", ICERequest{Participant: b.ID, Country: "XX"})
	if err != nil || second.Resource != "home" || second.Username == ice.Username {
		t.Fatal("credential isolation or home fallback broken")
	}
	if _, err := s.ICE("demonware", ICERequest{Participant: a.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign credentials issued")
	}
	if err := s.Release("nex", a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ICE("nex", ICERequest{Participant: a.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatal("released lease still issues credentials")
	}
}

func TestUnhealthyResourcesFailClosed(t *testing.T) {
	cfg := testConfig()
	for i := range cfg.Resources {
		cfg.Resources[i].Healthy = false
	}
	s := newService(t, cfg)
	p := register(t, s, "nex", registration("one"))
	if _, err := s.ICE("nex", ICERequest{Participant: p.ID}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("unhealthy TURN advertised")
	}
}

func TestLeaseExpiryAndClose(t *testing.T) {
	s := newService(t, testConfig())
	req := registration("one")
	req.TTLSeconds = 1
	p := register(t, s, "nex", req)
	// Use the real deadline, so authorization is checked even before a sweep.
	time.Sleep(time.Until(p.ExpiresAt) + 10*time.Millisecond)
	if _, err := s.Get("nex", p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired participant accessible")
	}
	if _, err := s.ICE("nex", ICERequest{Participant: p.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired ICE issued")
	}
	s.Close()
	s.Close()
	if _, err := s.Register("nex", registration("two")); !errors.Is(err, ErrUnavailable) {
		t.Fatal("closed service accepted registration")
	}
}

func socket(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func relayConfig(t *testing.T) Config {
	t.Helper()
	probe := socket(t)
	port := probe.LocalAddr().(*net.UDPAddr).Port
	probe.Close()
	cfg := testConfig()
	cfg.RelayBindIP = "127.0.0.1"
	cfg.RelayPublicIP = "127.0.0.1"
	cfg.RelayPortMin = port
	cfg.RelayPortMax = port
	return cfg
}

func makeRelay(t *testing.T, s *Service, a, b *net.UDPConn) (Relay, Participant, Participant) {
	t.Helper()
	p := register(t, s, "nex", registration("one"))
	q := register(t, s, "nex", registration("two"))
	r, err := s.AllocateRelay("nex", RelayRequest{Participants: [2]string{p.ID, q.ID}, TTLSeconds: 20})
	if err != nil {
		t.Fatal(err)
	}
	for i, conn := range []*net.UDPConn{a, b} {
		req := registration([]string{"one", "two"}[i])
		req.Candidates = []Candidate{{Endpoint: conn.LocalAddr().String(), Kind: "observed", Destination: r.Endpoint}}
		register(t, s, "nex", req)
	}
	return r, p, q
}

func send(t *testing.T, from *net.UDPConn, relay string, data string) {
	t.Helper()
	to, err := net.ResolveUDPAddr("udp", relay)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := from.WriteToUDP([]byte(data), to); err != nil {
		t.Fatal(err)
	}
}

func receive(t *testing.T, c *net.UDPConn, want string) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 100)
	n, _, err := c.ReadFromUDP(buf)
	if err != nil || string(buf[:n]) != want {
		t.Fatalf("receive got %q: %v", buf[:n], err)
	}
}

func silent(t *testing.T, c *net.UDPConn) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(75 * time.Millisecond))
	buf := make([]byte, 100)
	if _, _, err := c.ReadFromUDP(buf); err == nil {
		t.Fatal("unexpected forwarded packet")
	} else if e, ok := err.(net.Error); !ok || !e.Timeout() {
		t.Fatal(err)
	}
}

func TestSameIPRelayRejectsThirdPlayerAndUnverifiedMapping(t *testing.T) {
	s := newService(t, relayConfig(t))
	a, b, attacker := socket(t), socket(t), socket(t)
	r, p, q := makeRelay(t, s, a, b)
	send(t, a, r.Endpoint, "before-bind")
	silent(t, b)
	if _, err := s.BindRelay("demonware", r.ID, BindRequest{Endpoints: [2]string{a.LocalAddr().String(), b.LocalAddr().String()}}); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign bind allowed")
	}
	if _, err := s.BindRelay("nex", r.ID, BindRequest{Endpoints: [2]string{a.LocalAddr().String(), attacker.LocalAddr().String()}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("unverified mapping bound")
	}
	r, err := s.BindRelay("nex", r.ID, BindRequest{Endpoints: [2]string{a.LocalAddr().String(), b.LocalAddr().String()}})
	if err != nil || !r.Bound {
		t.Fatal(err)
	}
	send(t, attacker, r.Endpoint, "takeover")
	silent(t, a)
	silent(t, b)
	send(t, a, r.Endpoint, "host")
	receive(t, b, "host")
	send(t, b, r.Endpoint, "joiner")
	receive(t, a, "joiner")
	if err := s.Release("nex", p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindRelay("nex", r.ID, BindRequest{Endpoints: [2]string{a.LocalAddr().String(), b.LocalAddr().String()}}); !errors.Is(err, ErrNotFound) {
		t.Fatal("relay survived release")
	}
	if _, err := s.Get("nex", q.ID); err != nil {
		t.Fatal("other household player released")
	}
	send(t, b, r.Endpoint, "after-release")
	silent(t, a)
}

func TestRelayRequiresDestinationSpecificObservationAndInvalidatesOnUpdate(t *testing.T) {
	s := newService(t, relayConfig(t))
	a, b := socket(t), socket(t)
	r, _, _ := makeRelay(t, s, a, b)
	req := registration("two")
	req.Candidates = []Candidate{{Endpoint: b.LocalAddr().String(), Kind: "observed", Destination: "127.0.0.1:3478"}}
	register(t, s, "nex", req)
	bind := BindRequest{Endpoints: [2]string{a.LocalAddr().String(), b.LocalAddr().String()}}
	if _, err := s.BindRelay("nex", r.ID, bind); !errors.Is(err, ErrInvalid) {
		t.Fatal("other-destination mapping accepted")
	}
	req.Candidates[0].Destination = r.Endpoint
	register(t, s, "nex", req)
	if _, err := s.BindRelay("nex", r.ID, bind); err != nil {
		t.Fatal(err)
	}
	req.Candidates = nil
	register(t, s, "nex", req)
	send(t, a, r.Endpoint, "stale-mapping")
	silent(t, b)
	// A route change is admitted only after the adapter supplies a new verified
	// destination-specific observation; the old source port stops working.
	changed := socket(t)
	req.Candidates = []Candidate{{Endpoint: changed.LocalAddr().String(), Kind: "observed", Destination: r.Endpoint}}
	register(t, s, "nex", req)
	bind.Endpoints[1] = changed.LocalAddr().String()
	if _, err := s.BindRelay("nex", r.ID, bind); err != nil {
		t.Fatal(err)
	}
	send(t, b, r.Endpoint, "old-route")
	silent(t, a)
	send(t, changed, r.Endpoint, "new-route")
	receive(t, a, "new-route")
}

func TestRelayBandwidthLimitAndLargeDatagrams(t *testing.T) {
	cfg := relayConfig(t)
	cfg.RelayBytesPerSecond = 65507
	s := newService(t, cfg)
	a, b := socket(t), socket(t)
	r, _, _ := makeRelay(t, s, a, b)
	if _, err := s.BindRelay("nex", r.ID, BindRequest{Endpoints: [2]string{a.LocalAddr().String(), b.LocalAddr().String()}}); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("x", 60000)
	send(t, a, r.Endpoint, payload)
	send(t, a, r.Endpoint, payload)
	b.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 65535)
	n, _, err := b.ReadFromUDP(buf)
	if err != nil || string(buf[:n]) != payload {
		t.Fatal("large datagram truncated or lost", err, n)
	}
	silent(t, b)
}

func TestRelayCapacityAndLeaseExpiry(t *testing.T) {
	s := newService(t, relayConfig(t))
	a, b := socket(t), socket(t)
	r, p, q := makeRelay(t, s, a, b)
	third := register(t, s, "nex", registration("three"))
	if _, err := s.AllocateRelay("nex", RelayRequest{Participants: [2]string{p.ID, third.ID}, TTLSeconds: 10}); !errors.Is(err, ErrCapacity) {
		t.Fatal("port exhaustion not reported")
	}
	if retry, err := s.AllocateRelay("nex", RelayRequest{Participants: [2]string{p.ID, q.ID}, TTLSeconds: 10}); err != nil || retry.ID != r.ID {
		t.Fatal("retry consumed relay slot")
	}
	if _, err := s.BindRelay("nex", r.ID, BindRequest{Endpoints: [2]string{a.LocalAddr().String(), b.LocalAddr().String()}}); err != nil {
		t.Fatal(err)
	}
	req := registration("one")
	req.TTLSeconds = 1
	req.Candidates = []Candidate{{Endpoint: a.LocalAddr().String(), Kind: "observed", Destination: r.Endpoint}}
	p = register(t, s, "nex", req)
	time.Sleep(time.Until(p.ExpiresAt) + 10*time.Millisecond)
	send(t, a, r.Endpoint, "expired")
	silent(t, b)
	if _, err := s.BindRelay("nex", r.ID, BindRequest{Endpoints: [2]string{a.LocalAddr().String(), b.LocalAddr().String()}}); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired relay rebound")
	}
}
