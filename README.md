# netmon

Easy to set up tool for monitoring network traffic flows in your home network.

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
- [ ] Add pcap collector for other platforms
- [ ] Get local hostnames from DNS server
