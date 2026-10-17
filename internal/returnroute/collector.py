"""Fixed-target IPv4 ICMP route collection, executed by the existing Agent.

Uses only Python's standard library. No installation, listeners, credentials,
DNS lookup, arbitrary destinations, or persistent processes are required.
"""
import concurrent.futures
import json
import secrets
import socket
import struct
import sys
import time

TARGETS = {
    "beijing": [("CT", "219.141.140.10"), ("CU", "202.106.195.68"), ("CM", "221.179.155.161")],
    "shanghai": [("CT", "202.96.209.133"), ("CU", "210.22.97.1"), ("CM", "211.136.112.200")],
    "guangzhou": [("CT", "58.60.188.222"), ("CU", "210.21.196.6"), ("CM", "120.196.165.24")],
}


def checksum(data):
    if len(data) % 2:
        data += b"\0"
    value = sum(struct.unpack("!%dH" % (len(data) // 2), data))
    value = (value >> 16) + (value & 65535)
    value += value >> 16
    return (~value) & 65535


def matches(packet, target, identifier, sequence):
    if len(packet) < 28 or packet[0] >> 4 != 4:
        return False, False, False
    offset = (packet[0] & 15) * 4
    if offset < 20 or len(packet) < offset + 8:
        return False, False, False
    kind, code, _, received_id, received_seq = struct.unpack("!BBHHH", packet[offset:offset + 8])
    if kind == 0:
        return (received_id == identifier and received_seq == sequence
                and socket.inet_ntoa(packet[12:16]) == target), True, True
    if kind not in (3, 11) or (kind == 11 and code != 0):
        return False, False, False
    inner = packet[offset + 8:]
    if len(inner) < 28 or inner[0] >> 4 != 4 or inner[9] != socket.IPPROTO_ICMP:
        return False, False, False
    inner_offset = (inner[0] & 15) * 4
    if inner_offset < 20 or len(inner) < inner_offset + 8 or socket.inet_ntoa(inner[16:20]) != target:
        return False, False, False
    echo = struct.unpack("!BBHHH", inner[inner_offset:inner_offset + 8])
    return echo[0] == 8 and echo[3] == identifier and echo[4] == sequence, False, kind == 3


def trace(item):
    carrier, target = item
    result = {"carrier": carrier, "target": target, "reached": False, "hops": []}
    if not sys.platform.startswith("linux"):
        result["error"] = "unsupported_platform"
        return result
    try:
        connection = socket.socket(socket.AF_INET, socket.SOCK_RAW, socket.IPPROTO_ICMP)
    except PermissionError:
        result["error"] = "permission_denied"
        return result
    except OSError:
        result["error"] = "network_unavailable"
        return result
    identifier = secrets.randbelow(65535) + 1
    nonce = secrets.token_bytes(16)
    with connection:
        connection.settimeout(1.0)
        for ttl in range(1, 31):
            terminal = False
            hop = {"ttl": ttl}
            result["hops"].append(hop)
            payload = b"komari-route-v1" + nonce
            header = struct.pack("!BBHHH", 8, 0, 0, identifier, ttl)
            packet = struct.pack("!BBHHH", 8, 0, checksum(header + payload), identifier, ttl) + payload
            started = time.monotonic()
            deadline = started + 1.0
            try:
                connection.setsockopt(socket.IPPROTO_IP, socket.IP_TTL, ttl)
                connection.settimeout(1.0)
                connection.sendto(packet, (target, 0))
                while time.monotonic() < deadline:
                    connection.settimeout(max(0.001, deadline - time.monotonic()))
                    data, address = connection.recvfrom(4096)
                    matched, reached, ended = matches(data, target, identifier, ttl)
                    if not matched:
                        continue
                    hop.update(ip=address[0], rtt_ms=round((time.monotonic() - started) * 1000, 2))
                    result["reached"] = reached
                    terminal = ended
                    if ended and not reached:
                        result["error"] = "destination_unreachable"
                    break
            except socket.timeout:
                pass
            except OSError:
                result["error"] = "network_unavailable"
                break
            if result["reached"] or terminal:
                break
    return result


def main():
    city = sys.argv[1] if len(sys.argv) == 2 else ""
    if city not in TARGETS:
        raise SystemExit(2)
    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
        routes = list(pool.map(trace, TARGETS[city]))
    print("KOMARI_RETURN_ROUTE_V1")
    print(json.dumps({"schema": 1, "city": city, "routes": routes}, separators=(",", ":")), flush=True)


if __name__ == "__main__":
    main()
