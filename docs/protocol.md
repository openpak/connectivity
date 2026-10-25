# Protocol: connectivity

## What runs

- A typed Go API client in `client/`, also usable as a contract for C# adapters.

## Classic STUN responder (PS3 NAT check)

The PS3 firmware's NAT check sends a classic RFC 3489 Binding Request (no magic
cookie) to `us.np.stun.playstation.net:3478`; coturn ignores it. Set
`stun_listen` (local bind, e.g. `0.0.0.0:49161`), `stun_public_ip`,
`stun_alt_port` (alternate port, bound on the same host; it must be reachable
from the internet) and optionally `stun_public_port` (advertised primary port
when the host redirects 3478 to a different local port). Leave `stun_listen`
empty to disable it. Replies echo the transaction id and carry MAPPED-ADDRESS,
SOURCE-ADDRESS and CHANGED-ADDRESS, without MESSAGE-INTEGRITY (the firmware does
not check it). CHANGE-REQUEST change-port is answered from the other port;
change-IP cannot be honoured with one public IP (result: Type 2).

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
