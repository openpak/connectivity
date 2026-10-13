# NPLN rollout — 2026-10-13

Status: connectivity deployed; all 18 running NPLN title services integrated.
Seven games are owner-reported working after rollout. Remaining tests are deferred
at the owner's request; Switch NEX is the next family. No game client update is
needed for this ICE integration. Reverse hosting, rejoin and soak duration were
not individually reported and must not be inferred from a general “works”.

## Deployment

Production user systemd runs `openpak-connectivity.service` from a local, native
arm64 image. The initial rollout used `openpak.network` and bridge DNS.
At 2026-10-13 15:56–15:58 UTC, a guarded migration moved connectivity to host
networking so native UDP relay sockets preserve source endpoints. All 18 NPLN
containers remain bridged and now use `http://host.containers.internal:20150`.
Each passed health, unchanged-binary hash and authenticated register/ICE/release
through its mounted token and actual network namespace after its idle restart.
The API listens on IPv4 `0.0.0.0:20150`, protected by the existing INPUT firewall
(loopback/local host routes allowed; other API ingress rejected); an external
HTTP probe timed out. Do not add a public API permit.
An explicit persisted INPUT rule now rejects non-loopback TCP 20150 ahead of
other rules; all 18 authenticated ICE probes passed again with it in place.
Live service-issued Pia-shaped TURN allocation, channel binding and bidirectional
relay also passed after the migration.

The Duck experiment enables UDP 22900–22979 on the host, with the previously
deployed firewall rule. NPLN continues using existing coturn, unchanged.
Exact migration backups/journal live below this release directory in
`host-network-20261013T155605Z/`; candidate quadlet hashes in `manifest.json`
were refreshed to preserve the original guarded per-title rollback.
See `deploy/host_network.py` and Switch NEX `docs/duck-game.md` for the native
relay proof and the separate, unresolved stock-client mapping problem.

Private deployment root on the production host:
`~/openpak/deploy/connectivity/releases/20261013-npln/`.
It contains the service config, per-title tokens, pinned binaries, exact original
quadlet backups and an incrementally saved rollback manifest. These files stay
on the host; credentials and original runtime configuration are not in Git.

The service uses the existing home coturn secret and endpoints, without restarting
coturn. Each NPLN container has a distinct namespace/token, a private token-file
mount and the shared API URL. Candidate quadlets pin the previous base image by
image ID and mount the tested NPLN binary over `/app/app`; registry auto-update is
disabled while this candidate is under game testing. A Wants/After dependency
starts connectivity first without coupling running gameplay to its process lifetime.

Binary SHA-256:

- Connectivity: `25b45c320cfb2c7f3edcd3bd0f0c7cd8df40840489df7e3bb0f07dd9d8fd0c0e`.
- NPLN: `7eef7aef31c56f30e02a09548e44aab3a396215990c46c646822ec40b542b990`.

The NPLN candidate was built from workspace HEAD `ed41a6e` with the existing
local title additions/latency options retained, the new shared ICE adapter and a
keepalive timer race correction. The source tree is not a clean published release;
the running artifact is identified by its hash and retained on the host.

## Validation

- Connectivity and complete NPLN `go test -race ./...` and `go vet ./...` passed.
- Linux amd64 and arm64 builds passed.
- A live probe obtained two independent credentials from connectivity, used them
  against production coturn, and passed Pia-shaped ALLOCATE, CHANNEL-BIND and
  bidirectional UDP relay from the same household. Probe participants were released.
- Before each rolling restart, the title had no established TCP sessions.
- All 18 titles passed health, running-binary hash, API DNS/reachability and startup
  integration-marker checks. A final probe checked each container's actual mounted
  token against its namespace and registered/allocated/released a synthetic lease.
- This proves the deployed infrastructure and API path, not each game's gameplay.

| Title | Server/API verification | Post-rollout game acceptance |
| --- | --- | --- |
| Stardew Valley | Passed | Owner reports working |
| Bayonetta 3 | Passed | Pending |
| Core Keeper | Passed | Deferred, no result reported |
| Dinkum | Passed | Owner reports working |
| Endless Ocean Luminous | Passed | Pending |
| Go-Go Town! | Passed | Owner reports working |
| Hamster Playground | Passed | Pending |
| Human: Fall Flat | Passed | Owner reports working |
| Metal Gear Solid: Peace Walker | Passed | Pending |
| Monster Hunter Rise | Passed | Deferred: not installed on all devices |
| PlateUp! | Passed | Owner reports working |
| Pokémon Legends: Arceus | Passed | Pending; test the title's supported online flow |
| Pokémon Scarlet/Violet | Passed | Pending |
| Splatoon 3 | Passed | Switch connection failure reported; diagnosis deferred |
| Super Mario Bros. Wonder | Passed | Owner reports working |
| Super Mario Party Jamboree | Passed | Harbor phone crash prevented testing; owner attributes it to emulation |
| Terraria | Passed | Owner reports working |
| Wavecade | Passed | Pending |

## Deferred tests

The owner requested moving to the next family with the remaining games untested.
Bayonetta 3, Core Keeper, Endless Ocean Luminous, Hamster Playground, Peace Walker,
Pokémon Legends: Arceus, Pokémon Scarlet/Violet and Wavecade remain untested after
rollout. Monster Hunter Rise is also deferred; Jamboree is blocked by the reported
emulation crash, and Splatoon 3 has the reported Switch connection failure.

## Game testing method

First test Stardew Valley with separate devices and accounts: Switch hosting,
Harbor joining on the same internet connection. Confirm farm entry and game-state
exchange, play for 15 minutes, then leave/rejoin and reverse the host. Record each
result separately. Next test Go-Go Town! and PlateUp!, which have pre-rollout
two-device evidence, before titles with unresolved game-specific dependencies.
Different-household/CGNAT tests remain a separate acceptance case.

For server observation, look for `[Connectivity] ICE allocated by shared service`
in the title's logs and use namespace-scoped `/v1/status` through private operator
access. Do not copy bearer tokens, TURN passwords or raw player payloads into notes.
If a title fails, preserve the exact on-screen error and sanitized flow stage;
working login or a nonempty ICE response does not establish gameplay success.

## Guarded rollback

Run on production to restore one title's exact original quadlet and restart it:

```sh
python3 ~/openpak/deploy/connectivity/releases/20261013-npln/npln_rollout.py \
  rollback --root ~/openpak/deploy/connectivity/releases/20261013-npln \
  --title stardew-valley
```

Omit `--title` to restore all integrated NPLN units. The script refuses rollback
if a live quadlet differs from the candidate hash, protecting later operator
changes. It checks health after restoring. Connectivity remains deployed and
coturn remains untouched. Restarting a title loses its in-memory rooms and tokens;
rollback should be performed while that title is idle.
