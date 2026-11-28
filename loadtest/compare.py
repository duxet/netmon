"""Compares what netmon recorded with what went through the router setup.sh
built, per client, kind of traffic and direction.

usage: compare.py <netmon url> <interface counters json> [baseline]
"""
import json, re, subprocess, sys, urllib.request

base, baseline_path = sys.argv[1], sys.argv[2]
clients = {"192.168.77.2": "lan1", "192.168.77.3": "lan2"}
kinds = {8001: "tcp fin", 8002: "tcp rst", 8003: "udp"}

def ifstats():
    out = {}
    for ns in clients.values():
        d = {}
        for k in ("rx_bytes", "rx_packets", "tx_bytes", "tx_packets"):
            d[k] = int(open(f"/sys/class/net/v-{ns}/statistics/{k}").read())
        out[ns] = d
    return out

if len(sys.argv) > 3 and sys.argv[3] == "baseline":
    json.dump(ifstats(), open(baseline_path, "w"))
    sys.exit()

# Ground truth: iptables, in IP bytes.
truth = {}
rules = subprocess.run(["iptables", "-t", "mangle", "-L", "NETMON-LOAD", "-v", "-n", "-x"], capture_output=True, text=True).stdout
for line in rules.splitlines():
    m = re.match(r"\s*(\d+)\s+(\d+)\s+RETURN.*/\* (up|down) (\S+) (\d+) \*/", line)
    if m:
        pkts, nbytes, direction, client, port = m.groups()
        truth[(client, int(port), direction)] = (int(nbytes), int(pkts))

# netmon, from the flows of the last day.
req = urllib.request.Request(base + "/api/flows?limit=1000")
flows = json.load(urllib.request.urlopen(req))
seen = {}
for f in flows:
    src = f["Source"]["IPAddress"]
    if src not in clients or f["Port"] not in kinds:
        continue
    t = f["Traffic"]
    for direction, b, p in (("up", t["OutBytes"], t["OutPackets"]), ("down", t["InBytes"], t["InPackets"])):
        key = (src, f["Port"], direction)
        ob, op = seen.get(key, (0, 0))
        seen[key] = (ob + b, op + p)

print(f"{'client':6} {'kind':8} {'dir':4} {'router bytes':>14} {'netmon bytes':>14} {'missing':>8}   {'router pkts':>11} {'netmon pkts':>11}")
total_truth = total_seen = 0
for client, ns in clients.items():
    for port, kind in kinds.items():
        for direction in ("up", "down"):
            tb, tp = truth.get((client, port, direction), (0, 0))
            sb, sp = seen.get((client, port, direction), (0, 0))
            total_truth += tb
            total_seen += sb
            missing = (tb - sb) / tb * 100 if tb else 0
            print(f"{ns:6} {kind:8} {direction:4} {tb:14,} {sb:14,} {missing:7.3f}%   {tp:11,} {sp:11,}")
print(f"all: router {total_truth:,} netmon {total_seen:,} missing {(total_truth - total_seen) / total_truth * 100:.3f}%")

# Interface counters: Ethernet frames, so 14 bytes more per packet than IP.
baseline = json.load(open(baseline_path))
now = ifstats()
print()
for ns, d in now.items():
    b = baseline[ns]
    up = (d["rx_bytes"] - b["rx_bytes"]) - 14 * (d["rx_packets"] - b["rx_packets"])
    down = (d["tx_bytes"] - b["tx_bytes"]) - 14 * (d["tx_packets"] - b["tx_packets"])
    client = [c for c, n in clients.items() if n == ns][0]
    netmon_up = sum(v[0] for k, v in seen.items() if k[0] == client and k[2] == "up")
    netmon_down = sum(v[0] for k, v in seen.items() if k[0] == client and k[2] == "down")
    print(f"{ns}: interface up {up:,} down {down:,} (IP bytes) · netmon up {netmon_up:,} down {netmon_down:,}")
