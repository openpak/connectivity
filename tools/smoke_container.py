#!/usr/bin/env python3
"""Exercise a locally built image; leave no container or ephemeral credentials."""

import argparse
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import time
import urllib.error
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", default="localhost/openpak-connectivity:local")
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    (root / "bin").mkdir(exist_ok=True)
    run_id = secrets.token_hex(4)
    config = root / "bin" / f"smoke-{run_id}.json"
    name = f"openpak-connectivity-smoke-{run_id}"
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", 0))
        http_port = probe.getsockname()[1]
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as probe:
        probe.bind(("127.0.0.1", 0))
        relay_port = probe.getsockname()[1]
    token = secrets.token_hex(32)
    data = {
        "listen": f"127.0.0.1:{http_port}",
        "backend_tokens": {"smoke": token},
        "relay_bind_ip": "127.0.0.1",
        "relay_public_ip": "127.0.0.1",
        "relay_port_min": relay_port,
        "relay_port_max": relay_port,
        "allow_private_endpoints": True,
    }
    # Set permissions on creation, before writing even ephemeral credentials.
    with open(config, "x", opener=lambda p, f: os.open(p, f, 0o600)) as output:
        json.dump(data, output)
    started = False
    sockets = []
    try:
        subprocess.run(
            ["podman", "run", "-d", "--name", name, "--userns=keep-id",
             "--user", f"{os.getuid()}:{os.getgid()}", "--network=host",
             "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
             "-v", f"{config}:/app/config.json:ro", args.image],
            check=True, capture_output=True, text=True,
        )
        started = True
        base = f"http://127.0.0.1:{http_port}"
        for _ in range(100):
            try:
                with urllib.request.urlopen(base + "/healthz", timeout=0.2) as response:
                    assert json.load(response)["status"] == "ok"
                break
            except (OSError, urllib.error.URLError):
                time.sleep(0.1)
        else:
            raise RuntimeError("container health timed out")

        def api(method, path, body=None):
            encoded = json.dumps(body).encode() if body is not None else None
            request = urllib.request.Request(
                base + path, data=encoded, method=method,
                headers={"Authorization": "Bearer " + token,
                         "Content-Type": "application/json"},
            )
            with urllib.request.urlopen(request, timeout=3) as response:
                return json.load(response) if response.status != 204 else None

        requests, participants = [], []
        for i in range(2):
            udp = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
            udp.bind(("127.0.0.1", 0))
            udp.settimeout(2)
            sockets.append(udp)
            request = {
                "key": {"title": "smoke-game", "session": "one-room",
                        "player": str(i), "generation": "one", "component": "udp"},
                "ttl_seconds": 30, "candidates": [],
            }
            requests.append(request)
            participants.append(api("POST", "/v1/participants", request))
        relay = api("POST", "/v1/relays", {
            "participants": [p["id"] for p in participants], "ttl_seconds": 20,
        })
        endpoints = [f"127.0.0.1:{udp.getsockname()[1]}" for udp in sockets]
        for request, endpoint in zip(requests, endpoints):
            request["candidates"] = [{"kind": "observed", "endpoint": endpoint,
                                      "destination": relay["endpoint"]}]
            api("POST", "/v1/participants", request)
        bound = api("POST", f"/v1/relays/{relay['id']}/bind", {"endpoints": endpoints})
        assert bound["bound"]
        sockets[0].sendto(b"host", ("127.0.0.1", relay_port))
        assert sockets[1].recv(100) == b"host"
        sockets[1].sendto(b"joiner", ("127.0.0.1", relay_port))
        assert sockets[0].recv(100) == b"joiner"
        api("DELETE", f"/v1/participants/{participants[0]['id']}")
        assert api("GET", "/v1/status")["relays"] == 0
        subprocess.run(["podman", "stop", "-t", "10", name], check=True, capture_output=True)
        inspection = subprocess.run(["podman", "inspect", name], check=True,
                                    capture_output=True, text=True)
        assert json.loads(inspection.stdout)[0]["State"]["ExitCode"] == 0
        print("Container smoke passed: health, authenticated API, shared-IP UDP, release, shutdown.")
    finally:
        for udp in sockets:
            udp.close()
        if started:
            subprocess.run(["podman", "rm", "-f", name], capture_output=True)
        config.unlink(missing_ok=True)


if __name__ == "__main__":
    main()
