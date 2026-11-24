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

## Traffic by app

The dashboard and client pages show how much traffic went to popular services
(YouTube/Google, Telegram, X, Meta, Netflix, Apple, TikTok, Spotify, Twitch, Steam).
Apps are recognized by the remote IP address, using a built-in list of networks
each service operates. For better coverage, put `GeoLite2-ASN.mmdb` (from MaxMind)
next to the binary: addresses will then also be matched by the service's ASN.

Services that share infrastructure can't be told apart by IP (YouTube and other
Google services; Facebook, Instagram and WhatsApp), and traffic served from shared
CDNs such as Cloudflare or Akamai is reported as "Other".

## TODO
- [ ] Add collectors for other platforms (Windows, BSD)
- [ ] Get local hostnames from DNS server

## Development

The web UI lives in `client/` (React 19, Vite, Tailwind CSS v4, [shadcn/ui](https://ui.shadcn.com)
with shadcn charts on Recharts, TanStack Router/Query/Table). It is embedded into the Go binary
from `client/dist`, so build it before the server:

```sh
just build              # npm run build in client/, then go build
```

For UI work, run the API (`./netmon`, port 2137) and the Vite dev server, which proxies `/api`:

```sh
cd client && npm install && npm run dev
```

Add shadcn components with `npx shadcn@latest add <component>` from `client/`.
