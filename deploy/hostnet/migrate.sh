#!/bin/bash
# prds#166: move one bridge-published console UDP unit to Network=host.
# Usage: migrate.sh <unit-name-without-.container> [healthz-port|-] [KEY=VALUE ...]
# Backs up the quadlet + drop-ins, swaps Network/IP/PublishPort for Network=host, pins
# NEX_HTTP_ADDR=127.0.0.1:<healthz-port> when given, adds each KEY=VALUE as an Environment= line
# (a per-host port: RANKINGS_ADDR, an API address), links the AddHost drop-in. Does not restart.
set -euo pipefail
u=$1; http=${2:--}; shift $(( $# < 2 ? $# : 2 ))
Q=~/.config/containers/systemd/$u.container; H=~/openpak/deploy/hostnet
B=~/openpak/deploy/backups/hostnet-166/$u-$(date -u +%Y%m%dT%H%M%SZ); mkdir -p "$B"
cp -a "$Q" "$B/"; [ -d "$Q.d" ] && cp -a "$Q.d" "$B/"
grep -q "^Network=openpak.network$" "$Q" || { echo "unexpected Network in $Q" >&2; exit 1; }
sed -i -e "s/^Network=openpak.network$/# prds#166: host network so the service sees real client addresses (rootlessport rewrote them).\nNetwork=host/" -e "/^IP=/d" -e "/^PublishPort=/d" "$Q"
for e in "$@"; do k=${e%%=*}; sed -i "/^Environment=$k=/d" "$Q"; sed -i "/^Network=host$/a Environment=$e" "$Q"; done
[ "$http" != - ] && sed -i "/^Network=host$/a Environment=NEX_HTTP_ADDR=127.0.0.1:$http" "$Q"
mkdir -p "$Q.d"; ln -sfn "$H/hostnet-addhost.conf" "$Q.d/40-hostnet.conf"
echo "backup: $B"
