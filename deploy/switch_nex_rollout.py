#!/usr/bin/env python3
"""Deploy a guarded Switch NEX canary; preserve the existing NPLN configuration."""

import argparse
import json
from pathlib import Path
import secrets
import subprocess
import time
import urllib.request

import npln_rollout as ops


def health(port, path="healthz"):
    for _ in range(40):
        try:
            with urllib.request.urlopen(f"http://127.0.0.1:{port}/{path}", timeout=2) as response:
                body = response.read().decode()
                if body.startswith("ok ") or json.loads(body)["status"] == "ok":
                    return
        except OSError:
            time.sleep(0.5)
    raise RuntimeError("readiness failed")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("phase", choices=["deploy", "rollback"])
    parser.add_argument("--root", required=True, type=Path)
    parser.add_argument("--connectivity-root", required=True, type=Path)
    parser.add_argument("--title", default="mario-kart-8-deluxe")
    parser.add_argument("--prepared-tokens", action="store_true")
    args = parser.parse_args()
    root = args.root.expanduser().resolve()
    root.mkdir(parents=True, mode=0o700, exist_ok=True)
    units = Path.home() / ".config/containers/systemd"
    config_path = args.connectivity_root.expanduser().resolve() / "config.json"
    manifest_path = root / "manifest.json"
    if args.phase == "rollback":
        manifest = json.loads(manifest_path.read_text())
        path = Path(manifest["unit"])
        if ops.sha(path.read_bytes()) != manifest["candidate_sha256"]:
            raise RuntimeError("unit changed; refusing unguarded rollback")
        ops.private_write(path, (root / "original.container").read_text())
        ops.command("systemctl", "--user", "daemon-reload")
        ops.command("systemctl", "--user", "restart", manifest["service"])
        health(manifest["base"] + 2)
        manifest["state"] = "rolled_back"
        ops.private_write(manifest_path, json.dumps(manifest, indent=2))
        print("Original canary unit restored; connectivity and NPLN remain live.")
        return
    retry = manifest_path.exists() and json.loads(manifest_path.read_text()).get("state") == "rolled_back"
    if manifest_path.exists() and not retry:
        raise RuntimeError("deployment manifest already exists")
    registry = json.loads((root / "title-ports.json").read_text())
    name = "switch-" + args.title
    obj = ops.inspect(name)
    label = obj["Config"]["Labels"]["PODMAN_SYSTEMD_UNIT"]
    path = units / (label.removesuffix(".service") + ".container")
    base = registry[args.title]
    data = ops.command("podman", "exec", name, "sh", "-c", "cat /proc/net/tcp /proc/net/tcp6")
    for line in data.splitlines():
        fields = line.split()
        if len(fields) > 3 and fields[3] == "01" and int(fields[1].rsplit(":", 1)[1], 16) in (base, base + 1):
            raise RuntimeError("canary has active sessions; close the game before restart")
    original = path.read_text()
    ops.private_write(root / "original.container", original)
    config = json.loads(config_path.read_text())
    ops.private_write(root / "connectivity-config-before.json", json.dumps(config, indent=2))
    namespace = "switch-nex-" + args.title
    if namespace in config["backend_tokens"] and not retry and not args.prepared_tokens:
        raise RuntimeError("canary namespace already configured")
    token = config["backend_tokens"][namespace] if retry or args.prepared_tokens else secrets.token_hex(32)
    config["backend_tokens"][namespace] = token
    ops.private_write(root / "backend.token", token + "\n")
    if not args.prepared_tokens:
        ops.private_write(config_path, json.dumps(config, indent=2))
        ops.command("systemctl", "--user", "restart", "openpak-connectivity.service")
    health(20150)
    lines, section = [], ""
    for line in original.splitlines():
        if line.startswith("["):
            if section == "Container":
                lines.extend([f"Volume={root}/switch-nexd:/app/switch-nexd:ro",
                              f"Volume={root}/backend.token:/app/connectivity.token:ro,U",
                              "Environment=SWITCH_NEX_CONNECTIVITY_URL=http://127.0.0.1:20150",
                              "Environment=SWITCH_NEX_CONNECTIVITY_TOKEN_FILE=/app/connectivity.token"])
            section = line.strip("[]")
            lines.append(line)
            if section == "Unit":
                lines.extend(["Wants=openpak-connectivity.service", "After=openpak-connectivity.service"])
            continue
        if section == "Container":
            if line.startswith("Image="):
                line = "Image=sha256:" + obj["Image"].removeprefix("sha256:")
            if line.startswith("Environment=SWITCH_NEX_CONNECTIVITY_"):
                continue
            if line.startswith("AutoUpdate=") or (line.startswith("Volume=") and any(
                    destination in line.split(":")[1:2]
                    for destination in ("/app/switch-nexd", "/app/connectivity.token"))):
                continue
        lines.append(line)
    candidate = "\n".join(lines) + "\n"
    manifest = {"unit": str(path), "service": label, "container": name, "title": args.title,
                "base": base, "candidate_sha256": ops.sha(candidate.encode()),
                "binary_sha256": ops.sha((root / "switch-nexd").read_bytes()), "state": "activating"}
    ops.private_write(manifest_path, json.dumps(manifest, indent=2))
    ops.private_write(path, candidate)
    ops.command("systemctl", "--user", "daemon-reload")
    try:
        ops.command("systemctl", "--user", "restart", label)
        health(base + 2)
        actual = ops.command("podman", "exec", name, "sha256sum", "/app/switch-nexd").split()[0]
        if actual != manifest["binary_sha256"]:
            raise RuntimeError("running binary differs")
        logs = subprocess.run(["podman", "logs", name], capture_output=True, text=True, check=True)
        if "shared connectivity enabled for Switch NEX endpoint leases" not in logs.stdout + logs.stderr:
            raise RuntimeError("integration startup marker missing")
    except Exception as error:
        ops.private_write(path, original)
        ops.command("systemctl", "--user", "daemon-reload")
        ops.command("systemctl", "--user", "restart", label)
        manifest["state"] = "rolled_back"
        ops.private_write(manifest_path, json.dumps(manifest, indent=2))
        raise RuntimeError(f"canary failed ({type(error).__name__}); original unit restored") from None
    manifest["state"] = "integrated"
    ops.private_write(manifest_path, json.dumps(manifest, indent=2))
    print(f"{args.title}: healthy, pinned binary, shared leases, mandatory session tickets.")


if __name__ == "__main__":
    main()
