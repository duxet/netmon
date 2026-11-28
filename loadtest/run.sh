#!/usr/bin/env bash
#
# usage: run.sh path/to/netmon results-dir fin rst udp duration
#
# Runs netmon on a fresh database on the router setup.sh built, has both
# clients make fin TCP connections closed normally, rst reset ones and udp UDP
# exchanges per second each for duration (such as 3m), waits until conntrack
# forgot them and netmon recorded another round, then compares what netmon
# recorded with what went through the router. netmon's log, its memory and
# conntrack's size over time, and conntrack's own events are kept in
# results-dir.
set -uo pipefail

if [ $# -ne 6 ]; then
  sed -n '3,12p' "$0" >&2
  exit 2
fi
netmon=$(realpath "$1") dir=$2 fin=$3 rst=$4 udp=$5 duration=$6
here=$(cd "$(dirname "$0")" && pwd)
rm -rf "$dir" && mkdir -p "$dir" && dir=$(realpath "$dir")
port=21395
url=http://localhost:$port

(cd "$here/loadgen" && CGO_ENABLED=0 go build -o "$dir/loadgen" .) || exit 1
pkill -f 'loadgen server'
ip netns exec wan "$dir/loadgen" server -servers 203.0.113.2-65 > "$dir/server.log" 2>&1 &

conntrack -D -s 192.168.77.0/24 > /dev/null 2>&1
iptables -t mangle -Z NETMON-LOAD
python3 "$here/compare.py" $url "$dir/interfaces.json" baseline

cd "$dir"
NETMON_LISTEN=:$port NETMON_DATABASE="$dir/flows.db" "$netmon" > netmon.log 2>&1 &
pid=$!
until curl -fs -o /dev/null $url/api/status; do sleep 1; done

(
  while kill -0 $pid 2> /dev/null; do
    echo "$(date +%T) rss_kb=$(awk '/VmRSS/{print $2}' /proc/$pid/status) cpu_ticks=$(awk '{print $14+$15}' /proc/$pid/stat) conntrack=$(cat /proc/sys/net/netfilter/nf_conntrack_count)"
    sleep 5
  done
) > resources.log &

# Every connection's events with their IDs, from conntrack itself.
conntrack -E -o id -s 192.168.77.0/24 -b 33554432 > events.log 2> events.err &
events=$!

echo "$(date +%T) load: fin=$fin rst=$rst udp=$udp per second per client, for $duration"
for ns in lan1 lan2; do
  ip netns exec $ns "$dir/loadgen" client -servers 203.0.113.2-65 -fin "$fin" -rst "$rst" -udp "$udp" -duration "$duration" \
    > client-$ns.log 2> client-$ns.progress &
done
wait $(jobs -p | tail -2)
echo "$(date +%T) load done"
cat client-lan1.log client-lan2.log

# TIME_WAIT keeps closed connections for 2 minutes. Now and then the kernel
# keeps a reset one in CLOSE with the timeout of an open one, for days.
deadline=$((SECONDS + 300))
until [ "$(conntrack -L -s 192.168.77.0/24 2> /dev/null | wc -l)" -eq 0 ] || [ $SECONDS -gt $deadline ]; do sleep 5; done
conntrack -D -s 192.168.77.0/24 > /dev/null 2>&1
echo "$(date +%T) conntrack forgot the test's connections"
rounds() { curl -fs $url/metrics | awk '/^netmon_collector_rounds_total/{print $2}'; }
before=$(rounds)
until [ "$(rounds)" != "$before" ]; do sleep 5; done
sleep 5
echo "$(date +%T) recorded another round"

kill $events
curl -fs $url/metrics | grep -E '^netmon_collector_'
python3 "$here/compare.py" $url "$dir/interfaces.json"
echo "--- warnings and errors"
grep -E 'level=(WARN|ERROR)' netmon.log | grep -v 'GeoIP database\|ASN database'
echo "--- most memory"
sort -t= -k2 -n resources.log | tail -1
kill -INT $pid
wait $pid
