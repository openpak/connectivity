package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openpak/connectivity"
)

func TestClientEndToEnd(t *testing.T) {
	token := strings.Repeat("a", 32)
	s, err := connectivity.New(connectivity.Config{BackendTokens: map[string]string{"test": token}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	c, err := New(server.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p, err := c.Register(ctx, connectivity.RegisterRequest{Key: connectivity.ParticipantKey{Title: "game", Session: "room", Player: "one", Generation: "one", Component: "udp"}, TTLSeconds: 30})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Get(ctx, p.ID)
	if err != nil || got.ID != p.ID {
		t.Fatal("get failed")
	}
	if err := c.Report(ctx, connectivity.ReportRequest{Participant: p.ID, Stage: "registration", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Release(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	_, err = c.Get(ctx, p.ID)
	var api *APIError
	if !errors.As(err, &api) || api.Status != 404 {
		t.Fatal("status error lost")
	}
}

func TestClientDoesNotFollowCredentialRedirect(t *testing.T) {
	seen := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = true; w.WriteHeader(200) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer source.Close()
	c, _ := New(source.URL, strings.Repeat("a", 32))
	if _, err := c.Get(context.Background(), "one"); err == nil || seen {
		t.Fatal("redirect followed")
	}
}
