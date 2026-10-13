#!/usr/bin/env python3
"""Run on the deployment host: prepare service, integrate NPLN, or guarded rollback."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import subprocess
import time
import urllib.request


def command(*args):
    return subprocess.run(args, check=True, capture_output=True, text=True).stdout


def inspect(name):
    return json.loads(command("podman", "inspect", name))[0]


def environment(obj):
    return dict(x.split("=", 1) for x in obj["Config"]["Env"] if "=" in x)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def private_write(path, data):
    temporary = path.with_name(path.name + ".new")
    with open(temporary, "w", opener=lambda p, f: os.open(p, f, 0o600)) as output:
        output.write(data)
    os.replace(temporary, path)


def save(root, manifest):
    private_write(root / "manifest.json", json.dumps(manifest, indent=2))


def active_connections(name):
    # The pre-deployment gate avoids dropping real sessions during rolling restarts.
    data = command("podman", "exec", name, "sh", "-c", "cat /proc/net/tcp /proc/net/tcp6")
    return sum(1 for line in data.splitlines() if len(line.split()) > 3 and line.split()[3] == "01")


def wait_health(name, port):
    for _ in range(40):
        try:
            body = command("podman", "exec", name, "wget", "-qO-", f"http://127.0.0.1:{port}/health")
            if json.loads(body)["status"] == "ok":
                return
        except (subprocess.CalledProcessError, ValueError, KeyError):
            time.sleep(0.5)
    raise RuntimeError(f"health failed for {name}")


def prepare(root):
    if (root / "manifest.json").exists():
        raise RuntimeError("manifest already exists; use integrate or rollback")
    units = Path.home() / ".config/containers/systemd"
    if (units / "openpak-connectivity.container").exists():
        raise RuntimeError("connectivity unit already exists; refusing to replace it")
    containers = json.loads(command("podman", "ps", "--format", "json"))
    titles = []
    for item in containers:
        name = item["Names"][0] if isinstance(item["Names"], list) else item["Names"]
        obj = inspect(name)
        env = environment(obj)
        if "NPLN_TITLE" not in env:
            continue
        if active_connections(name):
            raise RuntimeError(f"active sessions on {name}; prepare when idle")
        path = units / f"{name}.container"
        original = path.read_bytes()
        backup = root / "backups" / path.name
        backup.write_bytes(original)
        backup.chmod(0o600)
        titles.append({"container": name, "title": env["NPLN_TITLE"],
                       "unit": str(path), "backup": str(backup),
                       "original_sha256": sha(original), "original_image": obj["Image"],
                       "health_port": int(env.get("HEALTH_LISTEN", ":21011").rsplit(":", 1)[1]),
                       "turn_host": env.get("NPLN_TURN_HOST"),
                       "stun_host": env.get("NPLN_STUN_HOST"),
                       "stun_port": int(env.get("NPLN_STUN_PORT", "3478")),
                       "turn_port": int(env.get("NPLN_TURN_PORT", "3478")), "state": "prepared"})
    if not titles:
        raise RuntimeError("no running NPLN titles")
    turn = inspect("openpak-turn")
    secret = next((x.split("=", 1)[1] for x in turn["Config"]["Cmd"]
                   if x.startswith("--static-auth-secret=")), "")
    if len(secret) < 32:
        raise RuntimeError("coturn secret unavailable or too short")
    first = titles[0]
    for title in titles:
        if environment(inspect(title["container"])).get("NPLN_TURN_SECRET") != secret:
            raise RuntimeError("NPLN/coturn secret mismatch")
        if any(title[k] != first[k] for k in ["turn_host", "stun_host", "turn_port", "stun_port"]):
            raise RuntimeError("multiple TURN resources need explicit configuration")
    tokens = {}
    for title in titles:
        token = secrets.token_hex(32)
        tokens["npln-" + title["title"]] = token
        token_file = root / "tokens" / (title["container"] + ".token")
        private_write(token_file, token + "\n")
        title["token_file"] = str(token_file)
    config = {"listen": "0.0.0.0:20150", "backend_tokens": tokens,
              "resources": [{"id": "home", "country": "IE", "home": True, "healthy": True,
                             "stun": f"{first['stun_host']}:{first['stun_port']}",
                             "turn": f"{first['turn_host']}:{first['turn_port']}", "turn_secret": secret}],
              "relay_port_min": 0, "relay_port_max": 0, "max_ttl_seconds": 3600}
    private_write(root / "config.json", json.dumps(config, indent=2))
    private_write(root / ".dockerignore", "*\n!Dockerfile.binary\n!connectivityd\n")
    image = "localhost/openpak-connectivity:" + root.name.lower()
    command("podman", "build", "-t", image, "-f", str(root / "Dockerfile.binary"), str(root))
    unit = f"""[Unit]
Description=OpenPak shared multiplayer connectivity
After=network-online.target
Wants=network-online.target

[Container]
Image={image}
ContainerName=openpak-connectivity
Network=openpak.network
UserNS=keep-id
User={os.getuid()}:{os.getgid()}
Volume={root}/config.json:/app/config.json:ro
PublishPort=127.0.0.1:20150:20150
ReadOnly=true
NoNewPrivileges=true
DropCapability=all

[Service]
Restart=always
TimeoutStartSec=90

[Install]
WantedBy=default.target
"""
    private_write(units / "openpak-connectivity.container", unit)
    manifest = {"release": root.name, "connectivity_image": image,
                "connectivity_sha256": sha((root / "connectivityd").read_bytes()),
                "npln_sha256": sha((root / "nplnd").read_bytes()), "titles": titles}
    save(root, manifest)
    command("systemctl", "--user", "daemon-reload")
    command("systemctl", "--user", "start", "openpak-connectivity.service")
    for _ in range(40):
        try:
            with urllib.request.urlopen("http://127.0.0.1:20150/healthz", timeout=2) as response:
                if json.load(response)["status"] == "ok":
                    print(f"Connectivity deployed; {len(titles)} NPLN units prepared, unchanged.")
                    return
        except OSError:
            time.sleep(0.5)
    raise RuntimeError("connectivity readiness failed")


def candidate_unit(original, title, root):
    sections, section = [], ""
    for line in original.splitlines():
        if line.startswith("["):
            if section == "Container":
                sections.extend([f"Volume={root}/nplnd:/app/app:ro",
                                 f"Volume={title['token_file']}:/app/connectivity.token:ro,U",
                                 "Environment=NPLN_CONNECTIVITY_URL=http://openpak-connectivity:20150",
                                 "Environment=NPLN_CONNECTIVITY_TOKEN_FILE=/app/connectivity.token"])
            section = line.strip("[]")
            sections.append(line)
            if section == "Unit":
                sections.extend(["Wants=openpak-connectivity.service", "After=openpak-connectivity.service"])
            continue
        if section == "Container":
            if line.startswith("Image="):
                line = "Image=sha256:" + title["original_image"].removeprefix("sha256:")
            if line.startswith("Volume=") and ":/app/app:" in line:
                continue
            if line.startswith("AutoUpdate="):
                continue
        sections.append(line)
    if section == "Container":
        raise RuntimeError("container section must precede Service/Install")
    return "\n".join(sections) + "\n"


def integrate(root, manifest):
    if sha((root / "nplnd").read_bytes()) != manifest["npln_sha256"]:
        raise RuntimeError("candidate binary changed")
    for title in sorted(manifest["titles"], key=lambda x: (x["title"] != "stardew-valley", x["title"])):
        if title["state"] == "integrated":
            continue
        name = title["container"]
        path = Path(title["unit"])
        if sha(path.read_bytes()) != title["original_sha256"]:
            raise RuntimeError(f"concurrent unit change for {name}")
        if active_connections(name):
            raise RuntimeError(f"active sessions on {name}; no restart performed")
        candidate = candidate_unit(Path(title["backup"]).read_text(), title, root)
        title["candidate_sha256"] = sha(candidate.encode())
        title["state"] = "activating"
        save(root, manifest)
        private_write(path, candidate)
        command("systemctl", "--user", "daemon-reload")
        try:
            command("systemctl", "--user", "restart", name + ".service")
            wait_health(name, title["health_port"])
            actual = command("podman", "exec", name, "sha256sum", "/app/app").split()[0]
            if actual != manifest["npln_sha256"]:
                raise RuntimeError("running binary differs")
            health = command("podman", "exec", name, "wget", "-qO-", "http://openpak-connectivity:20150/healthz")
            if json.loads(health)["status"] != "ok":
                raise RuntimeError("connectivity unreachable")
            log_result = subprocess.run(["podman", "logs", name], check=True,
                                        capture_output=True, text=True)
            logs = log_result.stdout + log_result.stderr
            if "shared connectivity enabled for NPLN ICE allocation" not in logs:
                raise RuntimeError("integration startup marker absent")
        except Exception:
            private_write(path, Path(title["backup"]).read_text())
            command("systemctl", "--user", "daemon-reload")
            command("systemctl", "--user", "restart", name + ".service")
            title["state"] = "rolled_back"
            save(root, manifest)
            raise RuntimeError(f"integration failed for {name}; original unit restored") from None
        title["state"] = "integrated"
        save(root, manifest)
        print(f"Integrated {title['title']}: health, binary hash and connectivity reachable.", flush=True)


def rollback(root, manifest, selected):
    for title in manifest["titles"]:
        if selected and title["title"] != selected:
            continue
        if title["state"] not in ("integrated", "activating"):
            continue
        path = Path(title["unit"])
        if sha(path.read_bytes()) != title.get("candidate_sha256"):
            raise RuntimeError("unit changed after rollout; refusing unguarded rollback")
        private_write(path, Path(title["backup"]).read_text())
        command("systemctl", "--user", "daemon-reload")
        command("systemctl", "--user", "restart", title["container"] + ".service")
        wait_health(title["container"], title["health_port"])
        title["state"] = "rolled_back"
        save(root, manifest)
        print("Restored " + title["title"])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("phase", choices=["prepare", "integrate", "rollback"])
    parser.add_argument("--root", required=True, type=Path)
    parser.add_argument("--title", help="title to restore; defaults to all integrated titles")
    args = parser.parse_args()
    root = args.root.expanduser().resolve()
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    for name in ["backups", "tokens"]:
        (root / name).mkdir(mode=0o700, exist_ok=True)
    if args.phase == "prepare":
        prepare(root)
        return
    manifest = json.loads((root / "manifest.json").read_text())
    if args.phase == "integrate":
        integrate(root, manifest)
    else:
        rollback(root, manifest, args.title)


if __name__ == "__main__":
    main()
