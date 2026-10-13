# Switch NEX Mario Kart 8 Deluxe canary

First candidate: rolled back after owner reported error 2306-0502 on Switch and Harbor.
At 12:07:34 UTC the production log showed successful authentication followed
by an empty WebSocket secure CONNECT, rejected by the new ticket requirement.
The original unit was restored and healthy at 12:08:04 UTC. The synthetic
ticket probe did not establish compatibility with physical clients; mandatory
tickets must not be rolled out to other titles based on that probe.

Second candidate: deployed with Mario Kart's secure station scheme corrected
from `prudp` to `prudps`, which selects SecureStream. The full race suite and
vet passed. Both LoginEx reply forms are now tested for that scheme and ticket
identity continuity, and the live probe verifies the advertised scheme before
spending tickets. Live probe passed. The owner confirmed successful host/join
and a completed race on Switch + Harbor. Production logs also show two clients
carrying 128-byte secure CONNECT payloads and a successful NAT punch. Reverse
hosting and reconnect behavior have not been separately confirmed. This proves
ordinary multiplayer compatibility, not native relay fallback.

Deployed on 2026-10-13 to `switch-mario-kart-8-deluxe` only. The other
28 Switch NEX titles retain their previous deployments.

The canary uses the shared connectivity API for endpoint leases and requires
session tickets for secure CONNECT. An empty secure CONNECT cannot acquire a
player identity through IP recall. Native Pia station metadata is preserved.

Validation: the full Switch NEX race suite and vet passed. The production
loopback probe passed ticket validation, rejection of empty CONNECT, two
same-IP logins spending their tickets in reverse order, independent endpoint
registration, and flat peer URL replies. Physical Switch/Harbor gameplay is
pending owner testing.

The first deployment attempt rolled back because the readiness script expected
JSON from the title's text health endpoint. After correcting the check, the
deployment passed readiness, binary hash, and integration startup checks.

Native Pia relay fallback is not implemented or advertised. Its authenticated
handshake still needs protocol evidence; this canary does not validate relay
fallback. Existing NPLN integration remains deployed.

Private release files and guarded rollback script reside on the production host
at `~/openpak/deploy/connectivity/releases/20261013-switch-nex/`.
