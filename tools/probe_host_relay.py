#!/usr/bin/env python3
"""External two-socket relay proof, with STUN and relay-destination observations.

Requires SSH operator access and sudo tcpdump on the server. The bearer token
stays on the server. This is an operator probe, not a stock-game mapping adapter.
"""
import argparse
import json
import os
import shlex
import socket
import struct
import subprocess

REMOTE = r'''
import json, os, socket, struct, subprocess, sys, tempfile, urllib.request
token_path, interface, nonce = sys.argv[1:]
token = open(os.path.expanduser(token_path)).read().strip()
def call(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Authorization": "Bearer " + token, "Content-Type": "application/json"}
    with urllib.request.urlopen(urllib.request.Request("http://127.0.0.1:20150" + path,
            data=data, method=method, headers=headers), timeout=5) as r:
        return json.load(r) if r.status == 200 else None
def emit(data):
    print(json.dumps(data), flush=True)
def reg(player, candidates):
    return call("POST", "/v1/participants", {"key": {
        "title": "duck-game", "session": "operator-relay-probe", "player": player,
        "generation": nonce, "component": "udp"}, "candidates": candidates, "ttl_seconds": 120})
participants, relay, capture = [], None, None
try:
    for player in ["probe-a", "probe-b"]:
        participants.append(reg(player, []))
    relay = call("POST", "/v1/relays", {"participants": [p["id"] for p in participants], "ttl_seconds": 120})
    port = int(relay["endpoint"].rsplit(":", 1)[1])
    with tempfile.TemporaryDirectory(prefix="connectivity-probe-") as directory:
        path = directory + "/observe.pcap"
        # A random 32-bit marker narrows capture; the whole nonce is checked below.
        marker = int.from_bytes(bytes.fromhex(nonce)[:4], "big")
        capture = subprocess.Popen(["sudo", "tcpdump", "-i", interface, "-U", "-c", "2", "-w", path,
            "udp dst port %d and udp[8:4] = %d" % (port, marker)], stderr=subprocess.PIPE)
        while True:
            line = capture.stderr.readline()
            if b"listening on" in line:
                break
            if not line:
                raise RuntimeError("capture failed to start")
        emit({"stage": "observe", "endpoint": relay["endpoint"]})
        capture.wait(timeout=20)
        data = subprocess.check_output(["sudo", "cat", path])
        endian = "<" if data[:4] in [b"\xd4\xc3\xb2\xa1", b"\x4d\x3c\xb2\xa1"] else ">"
        linktype = struct.unpack(endian + "I", data[20:24])[0]
        if linktype != 1:
            raise RuntimeError("probe requires Ethernet capture")
        offset, observed = 24, {}
        while offset < len(data):
            length = struct.unpack(endian + "I", data[offset+8:offset+12])[0]
            packet = data[offset+16:offset+16+length]
            offset += 16 + length
            if packet[12:14] != b"\x08\x00":
                raise RuntimeError("expected IPv4")
            ip = packet[14:]
            header = (ip[0] & 15) * 4
            udp = ip[header:]
            payload = udp[8:]
            for side in [0, 1]:
                if payload == bytes.fromhex(nonce) + bytes([side]):
                    observed[side] = socket.inet_ntoa(ip[12:16]) + ":" + str(struct.unpack("!H", udp[:2])[0])
        if len(observed) != 2:
            raise RuntimeError("relay-destination nonce observations missing")
        endpoints = [observed[i] for i in [0, 1]]
        for i, player in enumerate(["probe-a", "probe-b"]):
            reg(player, [{"endpoint": endpoints[i], "kind": "observed", "destination": relay["endpoint"]}])
        bound = call("POST", "/v1/relays/" + relay["id"] + "/bind", {"endpoints": endpoints})
        if not bound["bound"]:
            raise RuntimeError("bind failed")
        emit({"stage": "bound", "observed": endpoints})
        result = json.loads(sys.stdin.readline())
        status = call("GET", "/v1/status")
        emit({"stage": "status", "status": status, "traffic": result})
finally:
    if capture is not None and capture.poll() is None:
        capture.terminate()
        capture.wait(timeout=5)
    if relay:
        call("DELETE", "/v1/relays/" + relay["id"])
    for p in participants:
        call("DELETE", "/v1/participants/" + p["id"])
'''


def stun(sock, host):
    transaction = os.urandom(12)
    sock.sendto(struct.pack("!HHI12s", 1, 0, 0x2112A442, transaction), (host, 3478))
    data, source = sock.recvfrom(2048)
    if source != (host, 3478) or data[8:20] != transaction or data[:2] != b"\x01\x01":
        raise RuntimeError("invalid STUN response")
    offset = 20
    while offset + 4 <= len(data):
        kind, length = struct.unpack("!HH", data[offset:offset+4])
        value = data[offset+4:offset+4+length]
        if kind == 0x20 and len(value) >= 8 and value[1] == 1:
            port = struct.unpack("!H", value[2:4])[0] ^ 0x2112
            ip = socket.inet_ntoa(struct.pack("!I", struct.unpack("!I", value[4:8])[0] ^ 0x2112A442))
            return ip + ":" + str(port)
        offset += 4 + ((length + 3) & ~3)
    raise RuntimeError("STUN XOR-MAPPED-ADDRESS absent")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ssh", required=True, help="user@server")
    parser.add_argument("--public-ip", required=True)
    parser.add_argument("--interface", required=True)
    parser.add_argument("--token-file", required=True, help="server-side token path")
    args = parser.parse_args()
    nonce = os.urandom(16)
    sockets = [socket.socket(socket.AF_INET, socket.SOCK_DGRAM) for _ in range(3)]
    process = None
    try:
        for sock in sockets:
            sock.bind(("0.0.0.0", 0))
            sock.settimeout(5)
        mappings = [stun(sock, args.public_ip) for sock in sockets[:2]]
        remote = shlex.join(["python3", "-u", "-c", REMOTE, args.token_file, args.interface, nonce.hex()])
        process = subprocess.Popen(["ssh", "-o", "BatchMode=yes", args.ssh, remote],
                                   stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
        observation = json.loads(process.stdout.readline())
        ip, port = observation["endpoint"].rsplit(":", 1)
        endpoint = (ip, int(port))
        for side, sock in enumerate(sockets[:2]):
            sock.sendto(nonce + bytes([side]), endpoint)
        bound = json.loads(process.stdout.readline())
        if bound["observed"] != mappings:
            raise RuntimeError("STUN and relay-destination mappings differ; no success claim")
        for side in [0, 1]:
            payload = nonce + b"forward" + bytes([side])
            sockets[side].sendto(payload, endpoint)
            received, source = sockets[1-side].recvfrom(2048)
            if received != payload or source != endpoint:
                raise RuntimeError("forwarding payload/source mismatch")
        sockets[2].sendto(nonce + b"unbound", endpoint)
        sockets[1].settimeout(1)
        try:
            sockets[1].recvfrom(2048)
            raise RuntimeError("unbound source forwarded")
        except socket.timeout:
            pass
        process.stdin.write(json.dumps({"bidirectional": True, "unbound_dropped": True}) + "\n")
        process.stdin.flush()
        status = json.loads(process.stdout.readline())
        if process.wait(timeout=10):
            raise RuntimeError("remote cleanup failed")
        print(json.dumps({"stun_matches_relay_observations": True, "endpoint": observation["endpoint"], **status}, indent=2))
    finally:
        if process is not None and process.poll() is None:
            process.stdin.close()
            try:
                process.wait(timeout=25)
            except subprocess.TimeoutExpired:
                process.terminate()
        for sock in sockets:
            sock.close()


if __name__ == "__main__":
    main()
