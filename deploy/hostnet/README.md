# Host network for console UDP services (prds#166)

## Why

Rootless Podman 5.7 (the prod box, Ubuntu 26.04) forwards a `PublishPort=` on a bridge network
through `rootlessport`. Its child runs inside the container's network namespace and re-sends every
datagram from the container's own address. Measured 2026-11-09 on `openpak-tetris-axis`: the host
saw `37.228.205.125:5350 -> 10.0.0.249:22810`, while inside the container the same packet arrived
on `lo` as `10.89.2.46 -> 10.89.2.46:22810`. A NEX title then recorded every joiner as its own
bridge address and NAT traversal probed something nobody can reach. That was the Minecraft Wii U
118-0502/0521 (prds#162).

The source-preserving forwarder (`rootless_port_forwarder = "pasta"` in containers.conf) only
exists in Podman 6 with passt 2026_10+. No Ubuntu series packages those, so it is not an option on
this box. The documented way within 5.7 is to give the service a network namespace whose
forwarding keeps the source. Use `Network=host`, which the Switch NEX titles have run since
September (switch-nex `docs/deploy.md`), or `Network=pasta` (`openpak-edge`, `openpak-wfc-front`).
For console UDP services this repo uses `Network=host`.

## The pieces

| File | Box path | What |
| --- | --- | --- |
| `openpak-hostnet-bridge.container` | `~/.config/containers/systemd/` | Traefik on `openpak.network` publishing the TCP services that have no loopback port of their own (Postgres 5432, MinIO 9000, nn-account 20050/20051, nn-friends gRPC 20061) on `127.0.0.1`, **at the same port number** |
| `bridge/traefik.yml`, `bridge/dynamic.yml` | `~/openpak/deploy/hostnet/bridge/` | its entrypoints and TCP routers (one per port, plus matchmaking gRPC 20251), and the reverse direction below |
| `hostnet-addhost.conf` | `~/openpak/deploy/hostnet/`, symlinked as `<unit>.container.d/40-hostnet.conf` | `AddHost=openpak-<svc>:127.0.0.1` for every service reachable on loopback at its own port, so the names and ports in the shared `.env` keep working unchanged |
| `gen-bridge.py`, `rankings-ports.txt` | (run locally) | generate the three bridge files: edit the `loop`/`alias_tcp` lists or the rankings ports, run `python3 gen-bridge.py <dir>`, copy to the box, restart the bridge (DB blip ~3 s for every host-network unit) |
| `migrate.sh` | `~/openpak/deploy/hostnet/` | `migrate.sh <unit> [healthz-port|-] [KEY=VALUE ...]` moves one unit: backs up the quadlet and drop-ins to `~/openpak/deploy/backups/hostnet-166/<unit>-<UTC>`, swaps `Network=openpak.network`/`IP=`/`PublishPort=` for `Network=host`, optionally pins `NEX_HTTP_ADDR=127.0.0.1:<port>`, links the drop-in |

Names in the drop-in must be reachable on `127.0.0.1` at the port the `.env` uses: either the
service publishes `127.0.0.1:<port>:<port>` itself (account 20001, website, saves, mods, nx-baas
20070, chat, registry, rewards, leaderboards, achievements, title-storage, shop-core, matchmaking,
terraria-core, minecraft-core), or the bridge does. A new dependency: add a router + entrypoint +
`PublishPort` to the bridge (restart it, which blips DB for every host-network unit for ~3 s), then
the `AddHost` line.

## Callers that still use a moved service's name

A unit on the host network leaves `openpak.network`, so its container name stops resolving there.
For a caller on the bridge, the bridge takes the name back as a `NetworkAlias=` and routes it to
the service's own port on the host via `host.containers.internal`. The website's rankings reader
(`http://openpak-<title>:9090`, website `cmd/website/rankings.go`) works this way: each title now
listens on its own `RANKINGS_ADDR=:<base+7>`, because 22 titles all on `:9090` would clash on one
host, and an HTTP router `Host(openpak-<title>)` on the bridge's `:9090` sends it there. A single
other caller can instead get `AddHost=openpak-<svc>:host-gateway` in its own quadlet, as the website
has for `openpak-minecraft-core` (`MINECRAFT_URL`).

## Moving a service

1. Check its ports are free on the host. Under host networking the process binds them itself, next
   to the Switch titles. `ss -tulpn` for the UDP ports **and** for every TCP listener it opens. NEX
   opens healthz on base+2. animal-crossing-new-leaf's 22392 belonged to a Switch title, so it uses
   `NEX_HTTP_ADDR=127.0.0.1:22398`.
2. Check nobody reaches it **by container name** (Traefik `dynamic.yml`, switch-nex bridge, other
   quadlets' env). Those callers move to `host.containers.internal:<port>` first.
3. `~/openpak/deploy/hostnet/migrate.sh openpak-<svc> [healthz-port]`, `systemctl --user
   daemon-reload`, `systemctl --user restart openpak-<svc>`.
4. Verify: unit active; `podman inspect --format '{{.HostConfig.NetworkMode}}'` is `host`; `ss -ulpn`
   shows the service's own process on the UDP ports (not `rootlessport`); healthz answers; one UDP
   probe from the workstation shows its public IP in `sudo tcpdump -ni any udp dst port <p>`; no
   `connection refused`/`no such host` in the log.

Host firewall: the catch-all REJECT still covers any TCP listener the service now opens on the
host (healthz, APIs). Only the UDP ports it already had ACCEPT rules for are reachable.

## New console UDP services

Start them with `Network=host` and the `40-hostnet.conf` drop-in, never `PublishPort=…/udp` on
`openpak.network`. That loses the player's address.

## A loop entry that becomes the service itself

When the service a bridge loop entry points at moves to the host (nn-friends' gRPC 20061), the
bridge's `127.0.0.1:<port>` publish and the service would both want the port. Move the entry
from `loop` to `alias_tcp`, then: `migrate.sh`, `systemctl --user stop` the service, deploy and
restart the bridge, start the service (about 20 s of downtime for that service).

## Not moved, on purpose

- wfc-qr2, wfc-natneg: consoles' 27900/27901 are REDIRECTed to `openpak-wfc-front` (pasta, PROXY
  v2), which already hands them the real address; their direct publish is shadowed.

Photon (moved last): its health/erasure listener (`OPENPAK_Server__HealthPort`, default 8080) cannot
take 8080 on the host, where openpak-edge's pasta holds `10.0.0.249:8080`. It listens on 25080, and the
bridge maps `openpak-photon:8080` to it. Traefik's name-server route goes to
`host.containers.internal:9093`.

## Rollback (one unit)

```sh
B=$(ls -d ~/openpak/deploy/backups/hostnet-166/openpak-<svc>-* | tail -1)
cp -a "$B/openpak-<svc>.container" ~/.config/containers/systemd/
rm -f ~/.config/containers/systemd/openpak-<svc>.container.d/40-hostnet.conf
systemctl --user daemon-reload && systemctl --user restart openpak-<svc>
```

The bridge can stay; it only listens on loopback.
