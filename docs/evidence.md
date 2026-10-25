# Evidence: connectivity

## Verification

Verify after changes: an RFC 5389 request to 3478 must still be answered by
coturn (XOR-MAPPED-ADDRESS) and `tools/probe_deployed_turn.py` must pass.

## Tests

Tests exercise three real UDP sockets sharing one IP, rejected takeover packets,
destination-specific mapping checks, endpoint changes, expiry/release, concurrent
registration, namespace isolation, quotas, TURN credentials, strict HTTP parsing
and the typed API client. The container smoke tests health, authenticated API calls,
two-way UDP forwarding, release and graceful shutdown using ephemeral credentials.
These tests do not certify router NAT behaviour or real games.
