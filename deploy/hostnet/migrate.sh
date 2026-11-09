#!/bin/bash
# prds#166: move one bridge-published console UDP unit to Network=host. Usage: migrate.sh <unit-name-without-.container> [http-port]
# Backs up the quadlet + drop-ins, swaps Network/IP/PublishPort for Network=host, links the AddHost drop-in.
set -euo pipefail
u=$1; Q=~/.config/containers/systemd/$u.container; H=~/openpak/deploy/hostnet
B=~/openpak/deploy/backups/hostnet-166/$u-$(date -u +%Y%m%dT%H%M%SZ); mkdir -p "$B"
cp -a "$Q" "$B/"; [ -d "$Q.d" ] && cp -a "$Q.d" "$B/"
grep -q "^Network=openpak.network$" "$Q" || { echo "unexpected Network in $Q" >&2; exit 1; }
sed -i -e "s/^Network=openpak.network$/# prds#166: host network so the service sees real client addresses (rootlessport rewrote them).\nNetwork=host/" -e "/^IP=/d" -e "/^PublishPort=/d" "$Q"
if [ -n "${2:-}" ]; then sed -i "/^Network=host$/a Environment=NEX_HTTP_ADDR=127.0.0.1:$2" "$Q"; fi
mkdir -p "$Q.d"; ln -sfn "$H/hostnet-addhost.conf" "$Q.d/40-hostnet.conf"
echo "backup: $B"
