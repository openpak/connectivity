# connectivity

Standalone, console-neutral multiplayer connectivity service for OpenPak. Game
backends share endpoint leases, TURN credential issuance and native UDP relay
allocations through one authenticated internal HTTP API. AGPL-3.0-only; Go 1.26;
standard library only.

**Status: deployed 2026-10-13; all 18 running NPLN title services integrated.**
Infrastructure and live TURN probes pass; game acceptance starts with Stardew Valley.
See [deployment and testing status](docs/npln-deployment-2026-10-13.md).
This service preserves separate players behind a shared public IP. It does not
identify a player from an IP or automatically make stock games speak a new transport.
Game backends authenticate players and translate their native protocols at the API
boundary. The cross-family plan is [shared-connectivity-plan.md](../../docs/shared-connectivity-plan.md).

## What runs

- Internal HTTP API and health on `127.0.0.1:20150` by default.
- Participant leases keyed by backend namespace, title, room, player, connection
  generation and transport component; LAN, reported-public and observed candidates.
- TURN REST credentials for operator-configured regional coturn resources, with
  a healthy home fallback and a separate secret per resource.
- Optional UDP relay pool, one socket per participant pair. Allocations start
  inactive and require destination-specific verified endpoints before forwarding.
- Participant/relay expiry, backend quotas, per-relay bandwidth limits, stage
  outcome counters and graceful shutdown.
- A typed Go API client in `client/`, also usable as a contract for C# adapters.

## Run

```sh
cp config.example.json config.json
chmod 600 config.json
# Edit config.json: replace backend tokens and relay_public_ip before running.
GOWORK=off go run ./cmd/connectivityd -config ./config.json
```

Each token needs at least 32 bytes of independently generated random material;
never reuse a token across backends. Config files must be private (0600) and are
gitignored. Secrets and endpoint values are never written to ordinary logs.

Set both relay ports to `0` to disable the native UDP data plane. TURN allocation
is disabled until resources are configured. Add a resource such as:

```json
{
  "id": "home",
  "country": "IE",
  "stun": "stun.openpak.example:3478",
  "turn": "turn.openpak.example:3478",
  "turn_secret": "REPLACE_WITH_THE_COTURN_NODE_SECRET_AT_LEAST_32_BYTES",
  "healthy": true,
  "home": true
}
```

Only one home is allowed. Resource health is operator-configured at startup;
there is no automatic node discovery or live health probing yet. A country match
wins over home; unhealthy resources are excluded. Credentials expire with the
participant lease, using coturn's REST HMAC-SHA1 format.

`allow_private_endpoints` defaults to false for public relay mappings. Set it
only for a trusted local/test deployment. LAN candidates remain representable
without enabling it; they are never automatically chosen as public relay peers.

## API and adapter flow

All `/v1` calls require `Authorization: Bearer <backend token>`. JSON requests
use `Content-Type: application/json`. Each token owns its namespace and cannot
read, bind or release another backend's participants or relays.

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/healthz` | Process health; no authentication |
| POST | `/v1/participants` | Register or renew one exact participant generation |
| GET / DELETE | `/v1/participants/{id}` | Read / release an owned lease |
| POST | `/v1/ice` | Issue regional STUN/TURN endpoints and credentials |
| POST | `/v1/relays` | Allocate an inactive UDP pair relay |
| POST | `/v1/relays/{id}/bind` | Bind independently verified endpoint mappings |
| DELETE | `/v1/relays/{id}` | Close and release the relay socket |
| POST | `/v1/reports` | Record a bounded stage/outcome counter |
| GET | `/v1/status` | Backend-scoped lease counts, relay traffic and reports |

Register each player independently, even if public IP and local UDP ports match:

```json
{
  "key": {
    "title": "example-game", "session": "room-123", "player": "authenticated-id",
    "generation": "new-random-value-per-connection", "component": "game-udp"
  },
  "candidates": [
    {"endpoint": "192.168.1.10:3074", "kind": "lan"},
    {"endpoint": "203.0.113.20:41000", "kind": "reported_public"}
  ],
  "ttl_seconds": 300
}
```

The response supplies an opaque `id` and `expires_at`. Registering the same key
renews that ID and replaces its candidate set. A reconnect uses a new generation.
Candidate deduplication is confined to a participant and includes kind/destination.

ICE request: `{"participant":"<id>","country":"IE"}`. The adapter maps
`stun`, `turn`, `username`, `password` and `expires_at` into the client's existing
ICE response. Client response TTL/cache conventions remain the adapter's responsibility.

For native relays, allocate using
`{"participants":["<host-id>","<joiner-id>"],"ttl_seconds":300}`. Both
participants must be owned by the caller in the same title/session/component.
The allocation returns `id`, `endpoint`, `bound:false` and expiry. Then:

1. Verify each participant's native handshake and actual UDP mapping toward the
   allocated relay endpoint through an adapter/client path capable of doing so.
2. Renew each registration with an `observed` candidate containing `endpoint`
   and `destination` equal to that relay's endpoint.
3. Bind with `{"endpoints":["<host-ip:port>","<joiner-ip:port>"]}` in the
   same order as the allocation's participant IDs.

Only those exact source endpoints forward packets. A third player sharing the
same IP cannot claim the socket by sending first or by changing source port.
Different-destination STUN observations and reported/guessed ports do not bind.
Removing a bound observation suspends forwarding until a fresh verified bind.
Release closes associated relays immediately. Renewing a lease never silently
extends an existing relay's expiry; release/reallocate when its lease ends.

Native verification is a **backend responsibility**, not a new game wire
protocol provided by this service. This initial service has no native handshake
decoder or probe observation endpoint. An adapter must have a way to establish
destination-specific proof before using native relays; unsupported stock clients
must stay unsupported rather than fall back to IP-only learning. UDP endpoint
pinning also does not replace game-level authentication against on-path spoofing.

## Deploy and validate

`Dockerfile` builds amd64/arm64; `deploy/compose.yml` is a host-network deployment
template. Provision the private config so the container's UID 65532 can read it
(ownership mapping must match the container runtime). The API should remain on
loopback/private authenticated infrastructure; use a TLS reverse proxy for calls
across hosts. Never expose the bearer-token API to consoles or the public internet.
Publish only the configured UDP relay pool, with host/provider firewall rules.
The example uses `22900–22979/udp`; public IP and NAT forwarding must match.

```sh
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOWORK=off go build ./cmd/connectivityd
podman build -t localhost/openpak-connectivity:local .
python3 tools/smoke_container.py
```

Tests exercise three real UDP sockets sharing one IP, rejected takeover packets,
destination-specific mapping checks, endpoint changes, expiry/release, concurrent
registration, namespace isolation, quotas, TURN credentials, strict HTTP parsing
and the typed API client. The container smoke tests health, authenticated API calls,
two-way UDP forwarding, release and graceful shutdown using ephemeral credentials.
These tests do not certify router NAT behaviour or real games.

State is transient. Restart loses leases and native relay allocations; clients
must register again. Existing TURN credentials remain valid at coturn until their
encoded expiry even after release/restart; immediate TURN revocation requires
node-side session management, which is outside this initial service. There is no
fleet-wide TURN secret or automatic cross-region native-relay orchestration.
Budgeting is per backend and relay; adapters must additionally enforce per-player
and room limits before spending their backend quota.
