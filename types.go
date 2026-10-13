// Package connectivity supplies console-neutral endpoint leases and relay resources.
package connectivity

import "time"

type ParticipantKey struct {
	Title      string `json:"title"`
	Session    string `json:"session"`
	Player     string `json:"player"`
	Generation string `json:"generation"`
	Component  string `json:"component"`
}

type Candidate struct {
	Endpoint    string `json:"endpoint"`
	Kind        string `json:"kind"` // lan, reported_public, observed
	Destination string `json:"destination,omitempty"`
}

type RegisterRequest struct {
	Key        ParticipantKey `json:"key"`
	Candidates []Candidate    `json:"candidates"`
	TTLSeconds int            `json:"ttl_seconds"`
}

type Participant struct {
	ID         string         `json:"id"`
	Key        ParticipantKey `json:"key"`
	Candidates []Candidate    `json:"candidates"`
	ExpiresAt  time.Time      `json:"expires_at"`
}

type RelayRequest struct {
	Participants [2]string `json:"participants"`
	TTLSeconds   int       `json:"ttl_seconds"`
}

// BindRequest contains endpoints verified by the authenticated backend through its
// native handshake. A plain IP match or unauthenticated first UDP packet is not proof.
type BindRequest struct {
	Endpoints [2]string `json:"endpoints"`
}

type Relay struct {
	ID           string    `json:"id"`
	Endpoint     string    `json:"endpoint"`
	Participants [2]string `json:"participants"`
	Bound        bool      `json:"bound"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type ICERequest struct {
	Participant string `json:"participant"`
	Country     string `json:"country"`
}

type ICEResponse struct {
	Resource  string    `json:"resource"`
	STUN      string    `json:"stun"`
	TURN      string    `json:"turn"`
	Username  string    `json:"username"`
	Password  string    `json:"password"`
	ExpiresAt time.Time `json:"expires_at"`
}

type ReportRequest struct {
	Participant string `json:"participant"`
	Stage       string `json:"stage"`
	Outcome     string `json:"outcome"`
}

// Resource is operator-configured. Healthy resources are selectable; TURN secrets
// remain server-side and should be different for each region node.
type Resource struct {
	ID         string `json:"id"`
	Country    string `json:"country"`
	STUN       string `json:"stun"`
	TURN       string `json:"turn"`
	TURNSecret string `json:"turn_secret"`
	Healthy    bool   `json:"healthy"`
	Home       bool   `json:"home"`
}

type Config struct {
	Listen                    string            `json:"listen"`
	BackendTokens             map[string]string `json:"backend_tokens"` // namespace -> secret
	Resources                 []Resource        `json:"resources"`
	RelayBindIP               string            `json:"relay_bind_ip"`
	RelayPublicIP             string            `json:"relay_public_ip"`
	RelayPortMin              int               `json:"relay_port_min"`
	RelayPortMax              int               `json:"relay_port_max"`
	MaxParticipantsPerBackend int               `json:"max_participants_per_backend"`
	MaxRelaysPerBackend       int               `json:"max_relays_per_backend"`
	RelayBytesPerSecond       int               `json:"relay_bytes_per_second"`
	MaxTTLSeconds             int               `json:"max_ttl_seconds"`
	AllowPrivateEndpoints     bool              `json:"allow_private_endpoints"`
}
