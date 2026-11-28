# Load testing the Linux collector

These scripts simulate a busy router on a Linux machine, run netmon on it, and
compare what netmon records with what went through the router.

`setup.sh` builds the router in network namespaces: a LAN bridge with two clients,
and "the internet" behind a gateway, with 64 servers, reached through NAT, as from
a home router. It turns conntrack accounting and timestamps on, and iptables counts
what each client sends and receives of each kind of traffic, in IP bytes as conntrack
counts them. `run.sh` starts netmon on a fresh database, has both clients make many
short connections to random servers, of three kinds:

- TCP connections closed normally, which conntrack keeps for 2 minutes afterwards,
  so netmon's dump once a minute finds them,
- TCP connections closed with a reset, which conntrack forgets after 10 seconds,
- UDP exchanges, which conntrack forgets after 30 seconds,

so that netmon only learns how much the last two carried from conntrack's events
about connections closing. Once conntrack forgot all of them and netmon recorded
another round, it prints what netmon recorded next to iptables' and the interfaces'
counters, per client, kind and direction, and the most memory netmon used.
`teardown.sh` removes the router and restores the settings.

It needs root, Go, iproute2, iptables, conntrack-tools and Python 3, and changes the
machine's network settings while it runs, so use a virtual machine or one you can
spare:

```sh
go build -o /tmp/netmon ..       # in loadtest/, after building the web UI
sudo ./setup.sh
sudo ./run.sh /tmp/netmon /tmp/results 300 300 1000 3m
sudo ./teardown.sh
```

The numbers are connections a second per client: 300 closed normally, 300 reset and
1,000 UDP exchanges here, for 3 minutes. `results` keeps netmon's log, its memory
and conntrack's size every 5 seconds, and conntrack's own events. The interfaces'
counters are a few dozen bytes ahead of netmon's, for ARP, which conntrack doesn't
track.

## Results

On a virtual machine with 4 cores and Linux 6.18, which ran the clients and servers
too:

| New connections a second | Most tracked at once | Went through | netmon recorded | Lost events | Most memory |
|--------------------------|----------------------|--------------|-----------------|-------------|-------------|
| 600                      | 46,000               | 1.96 GB      | all, to the byte | none       | 139 MB      |
| 3,200                    | 173,000              | 7.99 GB      | all, to the byte | none       | 259 MB      |
| 6,100                    | 213,000              | 11.68 GB     | all, to the byte | none       | 359 MB      |

Memory is netmon's resident set, of which about 95 MB is there from the start; the most
is used for a moment while reading conntrack. The machine's clients couldn't make more
than about 6,100 connections a second.

Before these tests, the same runs found three problems, since fixed:

- At 3,200 connections a second, netmon left out 24% of the traffic: it kept at most
  65,536 closed connections between rounds, a bound meant for when rounds fail, while
  a minute had 192,000 of them.
- netmon used up to 724 MB, as it kept the kernel's full record of each connection, about
  500 bytes, several times over, and all of them at once while reading conntrack.
- Connections that reuse the ports of one that just closed get the same ID from the
  kernel, and netmon took them for one connection. That cost about 0.02% of the traffic
  at 600 connections a second.
