#!/usr/bin/env bash
#
# Builds a simulated router on this machine, in network namespaces: a LAN
# bridge br-lan (192.168.77.1/24) with the clients lan1 (.2) and lan2 (.3), and
# "the internet" in namespace wan, behind the gateway 198.51.100.2, with 64
# servers at 203.0.113.2-65, reached through v-wan with NAT. iptables counts
# what each client sends and receives of each kind of traffic, in IP bytes as
# conntrack counts them, to compare netmon with.
#
# Turns conntrack accounting and timestamps on; teardown.sh restores them.
# Needs root, iproute2, iptables and conntrack-tools.
set -euxo pipefail

state=/run/netmon-loadtest
mkdir -p $state
for setting in acct timestamp; do
  [ -f $state/$setting ] || sysctl -n net.netfilter.nf_conntrack_$setting > $state/$setting
  sysctl -w net.netfilter.nf_conntrack_$setting=1
done
sysctl -n net.ipv4.ip_forward > $state/ip_forward
sysctl -w net.ipv4.ip_forward=1

ip link add br-lan type bridge
ip addr add 192.168.77.1/24 dev br-lan
ip link set br-lan up

for i in 1 2; do
  ip netns add lan$i
  ip link add v-lan$i type veth peer name eth0 netns lan$i
  ip link set v-lan$i master br-lan up
  ip -n lan$i addr add 192.168.77.$((i + 1))/24 dev eth0
  ip -n lan$i link set eth0 up
  ip -n lan$i link set lo up
  ip -n lan$i route add default via 192.168.77.1
done

ip netns add wan
ip link add v-wan type veth peer name eth0 netns wan
ip addr add 198.51.100.1/24 dev v-wan
ip link set v-wan up
ip -n wan addr add 198.51.100.2/24 dev eth0
for a in $(seq 2 65); do ip -n wan addr add 203.0.113.$a/32 dev lo; done
ip -n wan link set eth0 up
ip -n wan link set lo up
ip -n wan route add default via 198.51.100.1
ip route add 203.0.113.0/24 via 198.51.100.2 dev v-wan

iptables -I FORWARD 1 -i br-lan -o v-wan -j ACCEPT
iptables -I FORWARD 1 -i v-wan -o br-lan -j ACCEPT
iptables -t nat -I POSTROUTING 1 -s 192.168.77.0/24 -o v-wan -j MASQUERADE

iptables -t mangle -N NETMON-LOAD
iptables -t mangle -I FORWARD 1 -j NETMON-LOAD
for c in 192.168.77.2 192.168.77.3; do
  for k in tcp:8001 tcp:8002 udp:8003; do
    proto=${k%:*} port=${k#*:}
    iptables -t mangle -A NETMON-LOAD -s $c -p $proto --dport $port -m comment --comment "up $c $port" -j RETURN
    iptables -t mangle -A NETMON-LOAD -d $c -p $proto --sport $port -m comment --comment "down $c $port" -j RETURN
  done
done
