package connectivity

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func request(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHTTPAuthStrictInputAndNoCredentialLeak(t *testing.T) {
	s := newService(t, testConfig())
	h := s.Handler()
	token := strings.Repeat("a", 32)
	if w := request(t, h, "GET", "/healthz", "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(t, h, "POST", "/v1/participants", "", `{}`); w.Code != 401 {
		t.Fatal("unauthenticated write")
	}
	if w := request(t, h, "GET", "/v1/status", "bad", ""); w.Code != 401 || strings.Contains(w.Body.String(), token) {
		t.Fatal("authentication leak")
	}
	for _, body := range []string{`{"unknown":1}`, `{} {}`, `null`, strings.Repeat("x", 20000)} {
		if w := request(t, h, "POST", "/v1/participants", token, body); w.Code != 400 {
			t.Fatalf("invalid input status %d", w.Code)
		}
	}
	b, _ := json.Marshal(registration("one"))
	w := request(t, h, "POST", "/v1/participants", token, string(b))
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("registration: %d", w.Code)
	}
	var p Participant
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	w = request(t, h, "GET", "/v1/participants/"+p.ID, strings.Repeat("b", 32), "")
	if w.Code != 404 || bytes.Contains(w.Body.Bytes(), []byte("192.168")) {
		t.Fatal("cross-backend endpoint leak")
	}
	w = request(t, h, "POST", "/v1/ice", token, `{"participant":"`+p.ID+`","country":"IE"}`)
	if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte(strings.Repeat("c", 32))) {
		t.Fatal("TURN secret exposed")
	}
	if w = request(t, h, "DELETE", "/v1/participants/"+p.ID, token, ""); w.Code != 204 {
		t.Fatal(w.Code)
	}
}

func TestHTTPReportsAreBoundedAndBackendScoped(t *testing.T) {
	s := newService(t, testConfig())
	h := s.Handler()
	p := register(t, s, "nex", registration("one"))
	if w := request(t, h, "POST", "/v1/reports", strings.Repeat("a", 32), `{"participant":"`+p.ID+`","stage":"gameplay","outcome":"success"}`); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := request(t, h, "POST", "/v1/reports", strings.Repeat("a", 32), `{"participant":"`+p.ID+`","stage":"free-form-log","outcome":"success"}`); w.Code != 400 {
		t.Fatal("arbitrary report accepted")
	}
	w := request(t, h, "GET", "/v1/status", strings.Repeat("b", 32), "")
	if strings.Contains(w.Body.String(), "gameplay") || strings.Contains(w.Body.String(), p.ID) {
		t.Fatal("foreign report exposed")
	}
	w = request(t, h, "GET", "/v1/status", strings.Repeat("a", 32), "")
	if !strings.Contains(w.Body.String(), `"gameplay:success":1`) {
		t.Fatal("report missing")
	}
}

func TestConcurrentRegistrationsAndRelease(t *testing.T) {
	s := newService(t, testConfig())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				p, err := s.Register("nex", registration("one"))
				if err != nil {
					t.Error(err)
					return
				}
				s.Get("nex", p.ID)
				s.Release("nex", p.ID)
			}
		}()
	}
	wg.Wait()
}

func TestConfigRejectsAmbiguousCredentials(t *testing.T) {
	for _, mutate := range []func(*Config){
		func(c *Config) { c.BackendTokens["nex"] = "short" },
		func(c *Config) { c.BackendTokens["demonware"] = c.BackendTokens["nex"] },
		func(c *Config) { c.Resources[1].ID = c.Resources[0].ID },
		func(c *Config) { c.Resources[1].Home = true },
		func(c *Config) { c.Resources[0].TURN = "bad:70000" },
		func(c *Config) { c.RelayBytesPerSecond = -1 },
		func(c *Config) {
			c.RelayPortMin = 30000
			c.RelayPortMax = 30001
			c.RelayBindIP = "0.0.0.0"
			c.RelayPublicIP = "127.0.0.1"
			c.AllowPrivateEndpoints = false
		},
	} {
		cfg := testConfig()
		mutate(&cfg)
		s, err := New(cfg)
		if err == nil {
			s.Close()
			t.Fatal("invalid configuration accepted")
		}
	}
}
