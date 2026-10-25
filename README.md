# Connectivity Server

OpenPak's Connectivity Server.

| | |
| --- | --- |
| Network | Console-neutral; serves any game backend |
| Role | Shared endpoint leases, TURN credentials and native UDP relay allocations for game backends, behind one authenticated internal HTTP API |
| Image | `localhost/openpak-connectivity` |
| Licence | AGPL-3.0-only |

## What it serves

| Capability | What |
| --- | --- |
| Participant leases | Register and renew endpoint candidates per backend namespace, title, room and player |
| ICE credentials | Regional STUN/TURN endpoints with coturn REST HMAC-SHA1 credentials |
| Native UDP relays | Optional pool, one socket per participant pair, inactive until verified destination-specific binds |
| Classic STUN | RFC 3489 Binding responder for the PS3 NAT check |
| Quotas and reports | Per-backend budgets, per-relay bandwidth limits and bounded stage counters |

## Run

```sh
cp config.example.json config.json   # replace backend tokens and relay_public_ip first
chmod 600 config.json
GOWORK=off go run ./cmd/connectivityd -config ./config.json
```

## Docs

- [Protocol](docs/protocol.md)
- [Evidence](docs/evidence.md)
- [Deploy](docs/deploy.md)
- [Notes](docs/notes.md)
- [NPLN rollout](docs/npln-deployment-2026-10-13.md)
- [Remaining game acceptance](docs/remaining-game-tests-2026-10-13.md)
- [Switch NEX Mario Kart 8 Deluxe canary](docs/switch-nex-canary-2026-10-13.md)
- [Switch NEX sequential rollout](docs/switch-nex-rollout-2026-10-13.md)

## Licence

AGPL-3.0-only. See [LICENSE](LICENSE).
