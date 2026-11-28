#!/usr/bin/env bash
#
# Undoes setup.sh.
set -x

pkill -f 'loadgen server'
iptables -t mangle -D FORWARD -j NETMON-LOAD
iptables -t mangle -F NETMON-LOAD
iptables -t mangle -X NETMON-LOAD
iptables -t nat -D POSTROUTING -s 192.168.77.0/24 -o v-wan -j MASQUERADE
iptables -D FORWARD -i br-lan -o v-wan -j ACCEPT
iptables -D FORWARD -i v-wan -o br-lan -j ACCEPT
for ns in lan1 lan2 wan; do ip netns del $ns; done
ip link del br-lan

state=/run/netmon-loadtest
for setting in acct timestamp; do
  [ -f $state/$setting ] && sysctl -w net.netfilter.nf_conntrack_$setting="$(cat $state/$setting)"
done
[ -f $state/ip_forward ] && sysctl -w net.ipv4.ip_forward="$(cat $state/ip_forward)"
rm -rf $state
