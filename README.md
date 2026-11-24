# netmon

Easy to set up tool for monitoring network traffic flows in your home network.

## TODO
- [ ] Add pcap collector for other platforms
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
