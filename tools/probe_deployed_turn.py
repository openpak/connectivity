#!/usr/bin/env python3
"""Verify service-issued credentials against live Pia-shaped TURN, without logging secrets."""

import argparse
import hashlib
import hmac
import json
import os
import shlex
import socket
import struct
import subprocess

MAGIC = 0x2112A442

REMOTE = r'''
import json,secrets,sys,urllib.request
from pathlib import Path
root,backend,action = sys.argv[1:4]
config=json.loads((Path(root)/"config.json").read_text())
token=config["backend_tokens"][backend]
def api(method,path,body=None):
    data=json.dumps(body).encode() if body is not None else None
    req=urllib.request.Request("http://127.0.0.1:20150"+path,data=data,method=method,
        headers={"Authorization":"Bearer "+token,"Content-Type":"application/json"})
    with urllib.request.urlopen(req,timeout=3) as response:
        return json.load(response) if response.status!=204 else None
if action=="allocate":
    results=[]
    for i in range(2):
        req={"key":{"title":"connectivity-probe","session":"turn-validation",
             "player":"synthetic-probe-"+str(i),"generation":secrets.token_hex(16),
             "component":"pia-ice"},"ttl_seconds":60,"candidates":[]}
        p=api("POST","/v1/participants",req)
        ice=api("POST","/v1/ice",{"participant":p["id"],"country":"IE"})
        results.append({"participant":p["id"],"ice":ice})
    print(json.dumps(results))
else:
    for participant in sys.argv[4:]:
        api("DELETE","/v1/participants/"+participant)
'''


def remote(args, action, ids=()):
    command = "python3 - " + " ".join(shlex.quote(v) for v in
                                       [args.release_directory, args.backend, action, *ids])
    result = subprocess.run(["ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
                             args.ssh_host, command], input=REMOTE, text=True,
                            capture_output=True, check=True)
    return json.loads(result.stdout) if action == "allocate" else None


def attr(kind, value):
    return struct.pack("!HH", kind, len(value)) + value + b"\0" * ((-len(value)) % 4)


def message(kind, before, after=(), key=None):
    tid = os.urandom(12)
    body = b"".join(before)
    if key is not None:
        header = struct.pack("!HHI", kind, len(body) + 24, MAGIC) + tid
        body += attr(0x0008, hmac.new(key, header + body, hashlib.sha1).digest())
    body += b"".join(after)
    return struct.pack("!HHI", kind, len(body), MAGIC) + tid + body


def parse(data, request):
    if len(data) < 20 or data[8:20] != request[8:20]:
        raise RuntimeError("invalid TURN transaction")
    kind, size, magic = struct.unpack("!HHI", data[:8])
    if magic != MAGIC or len(data) < 20 + size:
        raise RuntimeError("invalid STUN response")
    attributes, position = {}, 20
    while position + 4 <= 20 + size:
        attr_kind, length = struct.unpack("!HH", data[position:position + 4])
        attributes[attr_kind] = data[position + 4:position + 4 + length]
        position += 4 + length + (-length) % 4
    return kind, attributes


def address(value):
    if len(value) != 8 or value[1] != 1:
        raise RuntimeError("probe requires IPv4 TURN resource")
    ip = socket.inet_ntoa(struct.pack("!I", struct.unpack("!I", value[4:8])[0] ^ MAGIC))
    return ip, struct.unpack("!H", value[2:4])[0] ^ (MAGIC >> 16)


def encoded_address(peer):
    return struct.pack("!BBH", 0, 1, peer[1] ^ (MAGIC >> 16)) + struct.pack(
        "!I", struct.unpack("!I", socket.inet_aton(peer[0]))[0] ^ MAGIC)


class Allocation:
    def __init__(self, ice):
        host, port = ice["turn"].rsplit(":", 1)
        self.server = socket.gethostbyname(host), int(port)
        self.client = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        self.peer = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        self.client.settimeout(5)
        self.peer.settimeout(5)
        self.user = ice["username"].encode()
        self.password = ice["password"].encode()

    def transaction(self, request):
        self.client.sendto(request, self.server)
        data, source = self.client.recvfrom(65535)
        if source != self.server:
            raise RuntimeError("unexpected TURN responder")
        return parse(data, request)

    def allocate(self):
        transport = attr(0x0019, b"\x11\0\0\0")
        kind, values = self.transaction(message(0x0003, [], [transport]))
        if kind != 0x0113 or 0x0014 not in values or 0x0015 not in values:
            raise RuntimeError("TURN authentication challenge missing")
        realm, nonce = values[0x0014], values[0x0015]
        self.key = hashlib.md5(self.user + b":" + realm + b":" + self.password).digest()
        self.auth = [attr(0x0006, self.user), attr(0x0014, realm), attr(0x0015, nonce)]
        kind, values = self.transaction(message(0x0003, self.auth, [transport], self.key))
        if kind != 0x0103:
            raise RuntimeError("service credential rejected by TURN")
        self.relay = address(values[0x0016])

    def verify(self):
        request = message(0x0001, [])
        self.peer.sendto(request, self.server)
        data, source = self.peer.recvfrom(65535)
        if source != self.server:
            raise RuntimeError("unexpected STUN responder")
        kind, values = parse(data, request)
        if kind != 0x0101:
            raise RuntimeError("STUN discovery failed")
        peer = address(values[0x0020])
        channel = 0x4001
        trailing = [attr(0x000C, struct.pack("!HH", channel, 0)),
                    attr(0x0012, encoded_address(peer))]
        kind, _ = self.transaction(message(0x0009, self.auth, trailing, self.key))
        if kind != 0x0109:
            raise RuntimeError("Pia-shaped channel bind failed")
        self.peer.sendto(b"probe-peer", self.relay)
        received, source = self.client.recvfrom(65535)
        ch, length = struct.unpack("!HH", received[:4])
        if source != self.server or ch != channel or received[4:4 + length] != b"probe-peer":
            raise RuntimeError("peer-to-client relay failed")
        payload = b"probe-client"
        self.client.sendto(struct.pack("!HH", channel, len(payload)) + payload, self.server)
        received, _ = self.peer.recvfrom(65535)
        if received != payload:
            raise RuntimeError("client-to-peer relay failed")

    def close(self):
        self.client.close()
        self.peer.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ssh-host", required=True)
    parser.add_argument("--release-directory", required=True)
    parser.add_argument("--backend", default="npln-stardew-valley")
    args = parser.parse_args()
    allocations, leases = [], []
    try:
        leases = remote(args, "allocate")
        if leases[0]["ice"]["username"] == leases[1]["ice"]["username"]:
            raise RuntimeError("independent participant credentials collapsed")
        for lease in leases:
            allocation = Allocation(lease["ice"])
            allocations.append(allocation)
            allocation.allocate()
        for allocation in allocations:
            allocation.verify()
        print("Live TURN passed: two independent service credentials, Pia allocation/channel bind, bidirectional relay.")
    finally:
        for allocation in allocations:
            allocation.close()
        if leases:
            remote(args, "release", [lease["participant"] for lease in leases])


if __name__ == "__main__":
    main()
