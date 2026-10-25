# Notes: connectivity

## Overview

Standalone, console-neutral multiplayer connectivity service for OpenPak. Game
backends share endpoint leases, TURN credential issuance and native UDP relay
allocations through one authenticated internal HTTP API. AGPL-3.0-only; Go 1.26;
standard library only.

This service preserves separate players behind a shared public IP. It does not
identify a player from an IP or automatically make stock games speak a new transport.
Game backends authenticate players and translate their native protocols at the API
boundary. The cross-family plan is internal research notes.

## Status

**Status: deployed 2026-10-13; all 18 running NPLN title services integrated.**
Infrastructure and live TURN probes pass; game acceptance starts with Stardew Valley.
See [deployment and testing status](npln-deployment-2026-10-13.md).

## What runs

- Participant leases keyed by backend namespace, title, room, player, connection
  generation and transport component; LAN, reported-public and observed candidates.
- TURN REST credentials for operator-configured regional coturn resources, with
  a healthy home fallback and a separate secret per resource.
- Optional UDP relay pool, one socket per participant pair. Allocations start
  inactive and require destination-specific verified endpoints before forwarding.
- Participant/relay expiry, backend quotas, per-relay bandwidth limits, stage
  outcome counters and graceful shutdown.

## Production

Production (deployed 2026-10-21, re-verified 2026-10-23 after a
`systemctl --user restart openpak-connectivity`): local image
`localhost/openpak-connectivity:20261021-stun` (built on the box, not from CI),
config `~/openpak/deploy/connectivity/releases/20261021-stun/config.json`
(`stun_listen 0.0.0.0:49161`, `stun_alt_port 49160`, `stun_public_port 3478`).
The nft table comes from the root unit `openpak-stun-redirect.service`, which
loads `/etc/openpak/stun-redirect.nft`. It is separate from the iptables-nft
tables that netfilter-persistent restores. 49160/49161 already pass the host
INPUT rule and the Oracle security list (49152-65535). A real reboot has not been tested.

## Caveats

Caveat: coturn's relay range (`--min-port=49152`) includes 49160/49161. A
failed bind makes connectivityd exit, and the API goes down with it. If coturn
happens to hold one of those ports when connectivityd restarts, the unit
restart-loops until coturn frees the port. This is unlikely, but check it
with `ss -ulnp | grep -E ':4916[01] '` if the unit will not start.

## State and limits

State is transient. Restart loses leases and native relay allocations; clients
must register again. Existing TURN credentials remain valid at coturn until their
encoded expiry even after release/restart; immediate TURN revocation requires
node-side session management, which is outside this initial service. There is no
fleet-wide TURN secret or automatic cross-region native-relay orchestration.
Budgeting is per backend and relay; adapters must additionally enforce per-player
and room limits before spending their backend quota.
