# Changelog

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
