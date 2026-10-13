#!/usr/bin/env python3
"""Guarded production migration from bridge publishing to native host UDP sockets.

Run on the deployment host with --root pointing to the NPLN rollout release.
Retains a private journal and exact backups; never prints credentials.
"""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import time
import urllib.request

from npln_rollout import active_connections, command, private_write, wait_health

OLD = "http://openpak-connectivity:20150"
NEW = "http://host.containers.internal:20150"


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def api_probe(title):
    name = title["container"]
    # Read the actual mounted token inside the actual backend network namespace.
    def request(path, body):
        args = ["podman", "exec", name, "sh", "-c",
                'exec wget -T 5 -qO- --header="Authorization: Bearer $(cat /app/connectivity.token)" '
                '--header="Content-Type: application/json" "$@"', "probe",
                "--post-data=" + json.dumps(body)]
        args += [NEW + path]
        result = subprocess.run(args, capture_output=True, text=True)
        if result.returncode:
            raise RuntimeError("container API probe failed for " + name)
        return json.loads(result.stdout)

    health = json.loads(command("podman", "exec", name, "wget", "-T", "5", "-qO-", NEW + "/healthz"))
    if health.get("status") != "ok":
        raise RuntimeError("connectivity health failed")
    participant = request("/v1/participants", {
        "key": {"title": title["title"], "session": "host-network-check",
                "player": "operator-probe", "generation": str(time.time_ns()), "component": "ice"},
        "candidates": [], "ttl_seconds": 60})
    try:
        ice = request("/v1/ice", {"participant": participant["id"], "country": "IE"})
        if not all(ice.get(k) for k in ("stun", "turn", "username", "password")):
            raise RuntimeError("incomplete ICE response")
    finally:
        token = command("podman", "exec", name, "cat", "/app/connectivity.token").strip()
        req = urllib.request.Request("http://127.0.0.1:20150/v1/participants/" + participant["id"],
                                     method="DELETE", headers={"Authorization": "Bearer " + token})
        with urllib.request.urlopen(req, timeout=5) as response:
            assert response.status == 204


def migrate(root):
    manifest_path = root / "manifest.json"
    manifest = json.loads(manifest_path.read_text())
    titles = manifest["titles"]
    unit = Path.home() / ".config/containers/systemd/openpak-connectivity.container"
    config = root / "config.json"
    backup = root / ("host-network-" + time.strftime("%Y%m%dT%H%M%SZ", time.gmtime()))
    originals = {unit: unit.read_text(), config: config.read_text(), manifest_path: manifest_path.read_text()}
    for title in titles:
        path = Path(title["unit"])
        text = path.read_text()
        if text.count("Environment=NPLN_CONNECTIVITY_URL=" + OLD) != 1:
            raise RuntimeError("unexpected API URL for " + title["title"])
        if active_connections(title["container"]):
            raise RuntimeError("active sessions for " + title["title"])
        if digest(path) != title["candidate_sha256"]:
            raise RuntimeError("concurrent quadlet edit for " + title["title"])
        originals[path] = text
    if "Network=openpak.network" not in originals[unit]:
        raise RuntimeError("unexpected connectivity networking")
    # Existing INPUT policy must reject public API ingress. Local host routes
    # use loopback and are already permitted, including rootless bridge egress.
    rules = command("sudo", "iptables", "-S", "INPUT")
    if "-A INPUT -i lo -j ACCEPT" not in rules or not rules.rstrip().endswith("-A INPUT -j REJECT --reject-with icmp-host-prohibited"):
        raise RuntimeError("review host firewall before exposing a private API listener")
    if any("20150" in line and "ACCEPT" in line for line in rules.splitlines()):
        raise RuntimeError("unexpected API firewall accept rule")
    backup.mkdir(mode=0o700)
    journal = {"files": [], "restarted": [], "state": "prepared"}
    for index, (path, content) in enumerate(originals.items()):
        saved = backup / str(index)
        private_write(saved, content)
        journal["files"].append({"path": str(path), "backup": str(saved), "sha256": digest(path)})
    private_write(backup / "journal.json", json.dumps(journal, indent=2))
    restarted = []
    written = {}

    def replace(path, content):
        # Refuse edits or rollback that would overwrite another operator.
        expected = written.get(path, originals[path])
        if path.read_text() != expected:
            raise RuntimeError("concurrent change to " + path.name)
        private_write(path, content)
        written[path] = content

    try:
        for path, content in originals.items():
            if path.read_text() != content:
                raise RuntimeError("concurrent change to " + path.name)
        # Rootless containers reach the host alias through a local host route
        # (lo). Reject nonlocal ingress explicitly, independent of other rules.
        firewall = ["!", "-i", "lo", "-p", "tcp", "--dport", "20150", "-m", "comment",
                    "--comment", "OpenPak connectivity API local host only", "-j", "REJECT",
                    "--reject-with", "tcp-reset"]
        if subprocess.run(["sudo", "iptables", "-C", "INPUT", *firewall], capture_output=True).returncode:
            command("sudo", "iptables", "-I", "INPUT", "1", *firewall)
        command("sudo", "sh", "-c", "iptables-save > /etc/iptables/rules.v4")
        new_unit = "\n".join(line for line in originals[unit].replace("Network=openpak.network", "Network=host").splitlines()
                             if not line.startswith("PublishPort=")) + "\n"
        cfg = json.loads(originals[config])
        cfg["listen"] = "0.0.0.0:20150"
        replace(config, json.dumps(cfg, indent=2) + "\n")
        replace(unit, new_unit)
        command("systemctl", "--user", "daemon-reload")
        command("systemctl", "--user", "restart", "openpak-connectivity.service")
        for _ in range(40):
            try:
                with urllib.request.urlopen("http://127.0.0.1:20150/healthz", timeout=2) as response:
                    if json.load(response)["status"] == "ok":
                        break
            except OSError:
                time.sleep(.5)
        else:
            raise RuntimeError("host connectivity readiness failed")
        # Verify the new path before restarting any title.
        api_probe(titles[0])
        for title in titles:
            path = Path(title["unit"])
            if path.read_text() != originals[path] or active_connections(title["container"]):
                raise RuntimeError("title changed or acquired sessions: " + title["title"])
            candidate = originals[path].replace("Environment=NPLN_CONNECTIVITY_URL=" + OLD,
                                               "Environment=NPLN_CONNECTIVITY_URL=" + NEW)
            replace(path, candidate)
            restarted.append(title)
            command("systemctl", "--user", "daemon-reload")
            command("systemctl", "--user", "restart", title["container"] + ".service")
            wait_health(title["container"], title["health_port"])
            actual = command("podman", "exec", title["container"], "sha256sum", "/app/app").split()[0]
            if actual != manifest["npln_sha256"]:
                raise RuntimeError("NPLN binary changed")
            api_probe(title)
            title["candidate_sha256"] = digest(path)
            journal["restarted"].append(title["title"])
            private_write(backup / "journal.json", json.dumps(journal, indent=2))
            print(title["title"] + ": health, binary and authenticated ICE passed", flush=True)
        replace(manifest_path, json.dumps(manifest, indent=2))
        journal["state"] = "complete"
        private_write(backup / "journal.json", json.dumps(journal, indent=2))
        print("Host networking migration complete; backups: " + str(backup))
    except Exception:
        conflicts = [path.name for path, content in written.items() if path.read_text() != content]
        if conflicts:
            journal["state"] = "rollback_requires_operator"
            journal["conflicts"] = conflicts
            private_write(backup / "journal.json", json.dumps(journal, indent=2))
            raise RuntimeError("concurrent edits preserved; review migration journal before rollback") from None
        for path in written:
            private_write(path, originals[path])
        if unit in written or config in written:
            command("systemctl", "--user", "daemon-reload")
            command("systemctl", "--user", "restart", "openpak-connectivity.service")
        for title in restarted:
            command("systemctl", "--user", "restart", title["container"] + ".service")
            wait_health(title["container"], title["health_port"])
        journal["state"] = "rolled_back"
        private_write(backup / "journal.json", json.dumps(journal, indent=2))
        raise


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", required=True, type=Path)
    migrate(parser.parse_args().root.expanduser().resolve())
