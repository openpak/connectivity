package connectivity

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func splitServer(v string) (string, int, error) {
	host, p, err := net.SplitHostPort(v)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(p)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, ErrInvalid
	}
	return host, port, nil
}

// Handler exposes an internal API. It must be reachable only by backend adapters
// on an authenticated private network or through TLS, never directly by games.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			fail(w, ErrUnavailable)
			return
		}
		write(w, 200, map[string]string{"status": "ok"})
	})
	protect := func(fn func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			owner := s.authenticate(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || owner == "" {
				write(w, 401, map[string]string{"error": "unauthorized"})
				return
			}
			fn(w, r, owner)
		}
	}
	mux.HandleFunc("POST /v1/participants", protect(func(w http.ResponseWriter, r *http.Request, owner string) {
		var req RegisterRequest
		if !decode(w, r, &req) {
			return
		}
		p, err := s.Register(owner, req)
		respond(w, p, err)
	}))
	mux.HandleFunc("GET /v1/participants/{id}", protect(func(w http.ResponseWriter, r *http.Request, owner string) {
		p, err := s.Get(owner, r.PathValue("id"))
		respond(w, p, err)
	}))
	mux.HandleFunc("DELETE /v1/participants/{id}", protect(func(w http.ResponseWriter, r *http.Request, owner string) {
		if err := s.Release(owner, r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /v1/relays", protect(func(w http.ResponseWriter, r *http.Request, owner string) {
		var req RelayRequest
		if !decode(w, r, &req) {
			return
		}
		v, err := s.AllocateRelay(owner, req)
		respond(w, v, err)
	}))
	mux.HandleFunc("POST /v1/relays/{id}/bind", protect(func(w http.ResponseWriter, r *http.Request, owner string) {
		var req BindRequest
		if !decode(w, r, &req) {
			return
		}
		v, err := s.BindRelay(owner, r.PathValue("id"), req)
		respond(w, v, err)
	}))
	mux.HandleFunc("DELETE /v1/relays/{id}", protect(func(w http.ResponseWriter, r *http.Request, owner string) {
		if err := s.ReleaseRelay(owner, r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /v1/ice", protect(func(w http.ResponseWriter, r *http.Request, owner string) {
		var req ICERequest
		if !decode(w, r, &req) {
			return
		}
		v, err := s.ICE(owner, req)
		respond(w, v, err)
	}))
	mux.HandleFunc("POST /v1/reports", protect(func(w http.ResponseWriter, r *http.Request, owner string) {
		var req ReportRequest
		if !decode(w, r, &req) {
			return
		}
		if err := s.Report(owner, req); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("GET /v1/status", protect(func(w http.ResponseWriter, r *http.Request, owner string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.expireLocked(time.Now())
		participants, relays := 0, 0
		var bytes, dropped uint64
		for _, p := range s.participants {
			if p.owner == owner {
				participants++
			}
		}
		for _, relay := range s.relays {
			if relay.owner == owner {
				relays++
				bytes += relay.forwarded
				dropped += relay.dropped
			}
		}
		reports := map[string]uint64{}
		for k, v := range s.reports {
			if k.owner == owner {
				reports[k.stage+":"+k.outcome] = v
			}
		}
		write(w, 200, map[string]any{"participants": participants, "relays": relays, "active_relay_bytes": bytes, "active_relay_dropped": dropped, "reports": reports})
	}))
	return mux
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		write(w, 415, map[string]string{"error": "application/json required"})
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		fail(w, ErrInvalid)
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		fail(w, ErrInvalid)
		return false
	}
	return true
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	write(w, 200, v)
}
func fail(w http.ResponseWriter, err error) {
	status := 500
	message := "internal error"
	switch {
	case errors.Is(err, ErrInvalid):
		status, message = 400, ErrInvalid.Error()
	case errors.Is(err, ErrNotFound):
		status, message = 404, ErrNotFound.Error()
	case errors.Is(err, ErrCapacity):
		status, message = 429, ErrCapacity.Error()
	case errors.Is(err, ErrUnavailable):
		status, message = 503, ErrUnavailable.Error()
	}
	write(w, status, map[string]string{"error": message})
}
