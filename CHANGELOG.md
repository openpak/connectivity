# Changelog

## 2026-10-21 (v0.2.0)

- Add an optional classic (RFC 3489, cookie-less) STUN responder (`stun_listen`,
  `stun_public_ip`, `stun_public_port`, `stun_alt_port`; off by default) so the
  PS3 firmware NAT check (SceNetStun) passes with "NAT Type: Type 2". Binding
  requests are answered on a primary and an alternate UDP port with MAPPED-,
  SOURCE- and CHANGED-ADDRESS; change-port is honoured, change-IP cannot be (one
  public IP). Bounded reads, per-source rate limit, malformed packets dropped.
  Host rule `deploy/stun-redirect.nft` sends only cookie-less UDP 3478 to it;
  RFC 5389 traffic still reaches coturn unchanged. Replaces the hand-run
  stopgap on the production box.

## 2026-10-13

- Move production connectivity to host networking for source-preserving native
  UDP relays; migrate all 18 NPLN API paths to host.containers.internal and
  recheck health, binary hashes and each container's authenticated ICE flow.
  Add a guarded migration journal and a reproducible external relay probe with
  STUN plus exact-destination nonce observations, bidirectional forwarding and
  third-source rejection. Retail Duck relay mapping remains unimplemented.
- Deploy connectivity and integrate all 18 NPLN title services with private per-title
  credentials, sequential health/hash checks and guarded rollback. Live service-issued
  TURN credentials pass Pia-shaped allocation/channel binding and two-way UDP.
- Implement standalone participant leases, regional TURN credential allocation,
  verified endpoint UDP pair relays, authenticated internal API, typed Go client,
  race-tested household regressions and container/CI deployment templates.
