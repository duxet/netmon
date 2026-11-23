# netmon

Easy to set up tool for monitoring network traffic flows in your home network.

## Running on macOS

On macOS traffic is observed by capturing packets with libpcap (which ships
with macOS) and aggregating them into flows. Capturing requires access to the
BPF devices, so start netmon as root:

```sh
sudo ./netmon
```

It captures on the interface the default route goes through. Set
`NETMON_INTERFACE` to capture on another one:

```sh
sudo NETMON_INTERFACE=en5 ./netmon
```

Things to know:

- Only traffic that reaches the chosen interface is recorded. For most Macs
  that is their own traffic; the traffic of other hosts shows up when the Mac
  routes or bridges it (e.g. Internet Sharing), or is plugged into a mirrored
  switch port.
- MAC addresses are recorded for hosts on the interface's local network only.
  Interfaces without Ethernet frames, such as VPN tunnels, give no MAC
  addresses, so there will be no clients listed for them.
- Building needs cgo and the Xcode command line tools (`xcode-select --install`).
- `flows.db` is created in the working directory, so it will be owned by root
  when started with `sudo`.

## TODO
- [ ] Add collectors for other platforms (Windows, BSD)
- [ ] Get local hostnames from DNS server
