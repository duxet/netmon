import type { Client, Endpoint, Flow, Traffic } from "@/lib/api"

export const EMPTY_TRAFFIC: Traffic = {
  InBytes: 0,
  InPackets: 0,
  OutBytes: 0,
  OutPackets: 0,
}

export function addTraffic(a: Traffic, b: Traffic): Traffic {
  return {
    InBytes: a.InBytes + b.InBytes,
    InPackets: a.InPackets + b.InPackets,
    OutBytes: a.OutBytes + b.OutBytes,
    OutPackets: a.OutPackets + b.OutPackets,
  }
}

/** Reverse-DNS names come back fully qualified ("nas.lan."). */
export function cleanHostname(hostname: string | null | undefined): string | null {
  if (!hostname) return null
  return hostname.endsWith(".") ? hostname.slice(0, -1) : hostname
}

const MAC_PATTERN = /^([0-9a-f]{2}[:-]){5}[0-9a-f]{2}$/i

export function isMacAddress(value: string): boolean {
  return MAC_PATTERN.test(value)
}

/**
 * Locally administered addresses, such as the private per-network addresses
 * phones and laptops pick, belong to no manufacturer.
 */
export function isRandomizedMac(mac: string): boolean {
  const firstOctet = Number.parseInt(mac.slice(0, 2), 16)
  return Number.isFinite(firstOctet) && (firstOctet & 0x02) !== 0
}

export function normalizeMac(value: string): string {
  return value.toLowerCase().replaceAll("-", ":")
}

// ---------------------------------------------------------------------------
// Devices: /api/clients returns one row per (MAC, IP) pair. A device is a MAC
// with all of the addresses it used.
// ---------------------------------------------------------------------------

export interface DeviceAddress {
  ip: string
  hostname: string | null
  /** The network the address belongs to, if any. */
  networkId: number | null
  traffic: Traffic
}

export interface Device {
  mac: string
  /** The name the user gave the device. */
  name: string | null
  /** First reverse-DNS name found across the device's addresses. */
  hostname: string | null
  /** Manufacturer of the network interface, from its MAC address. */
  vendor: string | null
  addresses: DeviceAddress[]
  /** IDs of the networks the device's addresses belong to, busiest address first. */
  networks: number[]
  traffic: Traffic
  /** Bytes received by the device (download). */
  download: number
  /** Bytes sent by the device (upload). */
  upload: number
  total: number
  packets: number
}

export function groupDevices(clients: Client[]): Device[] {
  const byMac = new Map<string, Device>()

  for (const client of clients) {
    const mac = normalizeMac(client.Endpoint.MACAddress)
    const hostname = cleanHostname(client.Hostname)
    let device = byMac.get(mac)
    if (!device) {
      device = {
        mac,
        name: client.Name,
        hostname: null,
        vendor: client.Vendor,
        addresses: [],
        networks: [],
        traffic: EMPTY_TRAFFIC,
        download: 0,
        upload: 0,
        total: 0,
        packets: 0,
      }
      byMac.set(mac, device)
    }
    device.hostname ??= hostname
    device.addresses.push({
      ip: client.Endpoint.IPAddress,
      hostname,
      networkId: client.NetworkID ?? null,
      traffic: client.Traffic,
    })
    device.traffic = addTraffic(device.traffic, client.Traffic)
  }

  const devices = [...byMac.values()]
  for (const device of devices) {
    // Client rows come from flows the device originated, so the reply
    // direction (In) is what it downloaded.
    device.download = device.traffic.InBytes
    device.upload = device.traffic.OutBytes
    device.total = device.download + device.upload
    device.packets = device.traffic.InPackets + device.traffic.OutPackets
    device.addresses.sort(
      (a, b) =>
        b.traffic.InBytes + b.traffic.OutBytes - (a.traffic.InBytes + a.traffic.OutBytes)
    )
    device.networks = [
      ...new Set(device.addresses.flatMap((address) => (address.networkId === null ? [] : [address.networkId]))),
    ]
  }
  return devices.sort((a, b) => b.total - a.total)
}

/** The user's name for a device, else its hostname, IP or MAC address. */
export function deviceLabel(device: Pick<Device, "name" | "hostname" | "addresses" | "mac">): string {
  return device.name ?? device.hostname ?? device.addresses[0]?.ip ?? device.mac
}

/** Labels of named or resolved local devices, by each of their IP addresses. */
export function deviceLabelsByIp(devices: Device[]): Map<string, string> {
  const labels = new Map<string, string>()
  for (const device of devices) {
    if (!device.name && !device.hostname) continue
    for (const address of device.addresses) labels.set(address.ip, deviceLabel(device))
  }
  return labels
}

export function findDevice(devices: Device[], id: string): Device | undefined {
  if (isMacAddress(id)) {
    const mac = normalizeMac(id)
    return devices.find((device) => device.mac === mac)
  }
  return devices.find((device) => device.addresses.some((address) => address.ip === id))
}

// ---------------------------------------------------------------------------
// Flows seen from one subject (a device MAC or an IP address).
// ---------------------------------------------------------------------------

export type FlowDirection = "outbound" | "inbound"

export interface SubjectFlow {
  id: string
  direction: FlowDirection
  /** The subject's own endpoint in this flow. */
  self: Endpoint
  /** The endpoint on the other side of the subject. */
  peer: Endpoint
  protocol: number
  port: number
  service: string
  /** Bytes received by the subject. */
  download: number
  /** Bytes sent by the subject. */
  upload: number
  total: number
  packets: number
}

export type Subject = { kind: "mac"; mac: string } | { kind: "ip"; ip: string }

export function parseSubject(id: string): Subject {
  return isMacAddress(id) ? { kind: "mac", mac: normalizeMac(id) } : { kind: "ip", ip: id }
}

function isSubject(endpoint: Endpoint, subject: Subject): boolean {
  return subject.kind === "mac"
    ? normalizeMac(endpoint.MACAddress) === subject.mac
    : endpoint.IPAddress === subject.ip
}

/**
 * Conntrack records the originator as Source: "Out" counters are what the
 * source sent and "In" counters are the replies. Re-orient each flow so
 * download/upload are always from the subject's point of view.
 */
export function toSubjectFlows(flows: Flow[], subject: Subject): SubjectFlow[] {
  const seen = new Map<string, number>()
  return flows.map((flow) => {
    const outbound = isSubject(flow.Source, subject) || !isSubject(flow.Destination, subject)
    const t = flow.Traffic
    const download = outbound ? t.InBytes : t.OutBytes
    const upload = outbound ? t.OutBytes : t.InBytes
    const self = outbound ? flow.Source : flow.Destination
    const peer = outbound ? flow.Destination : flow.Source
    // Stable across refetches so table state (page, hover) survives updates.
    const baseId = `${self.IPAddress}>${peer.IPAddress}:${flow.IPProto}/${flow.Port}:${outbound ? "o" : "i"}`
    const duplicates = seen.get(baseId) ?? 0
    seen.set(baseId, duplicates + 1)
    return {
      id: duplicates ? `${baseId}#${duplicates}` : baseId,
      direction: outbound ? "outbound" : "inbound",
      self,
      peer,
      protocol: flow.IPProto,
      port: flow.Port,
      service: serviceLabel(flow.IPProto, flow.Port),
      download,
      upload,
      total: download + upload,
      packets: t.InPackets + t.OutPackets,
    }
  })
}

// ---------------------------------------------------------------------------
// Protocols, services and countries
// ---------------------------------------------------------------------------

const PROTOCOLS: Record<number, string> = {
  1: "ICMP",
  2: "IGMP",
  6: "TCP",
  17: "UDP",
  41: "IPv6",
  47: "GRE",
  50: "ESP",
  51: "AH",
  58: "ICMPv6",
  132: "SCTP",
}

/** Parses a "protocol/port" service key from /api/flows/summary, e.g. "6/443". */
export function parseServiceKey(key: string): { protocol: number; port: number } {
  const [protocol, port] = key.split("/").map(Number)
  return { protocol: protocol || 0, port: port || 0 }
}

export function protocolName(protocol: number): string {
  return PROTOCOLS[protocol] ?? `IP ${protocol}`
}

const SERVICES: Record<string, string> = {
  "tcp/20": "FTP data",
  "tcp/21": "FTP",
  "tcp/22": "SSH",
  "tcp/23": "Telnet",
  "tcp/25": "SMTP",
  "tcp/53": "DNS",
  "udp/53": "DNS",
  "udp/67": "DHCP",
  "udp/68": "DHCP",
  "tcp/80": "HTTP",
  "tcp/110": "POP3",
  "udp/123": "NTP",
  "tcp/143": "IMAP",
  "udp/137": "NetBIOS",
  "udp/138": "NetBIOS",
  "tcp/139": "NetBIOS",
  "tcp/445": "SMB",
  "tcp/443": "HTTPS",
  "udp/443": "QUIC",
  "tcp/465": "SMTPS",
  "udp/500": "IKE",
  "tcp/554": "RTSP",
  "tcp/587": "SMTP",
  "tcp/853": "DNS over TLS",
  "udp/853": "DNS over QUIC",
  "tcp/993": "IMAPS",
  "tcp/995": "POP3S",
  "udp/1194": "OpenVPN",
  "tcp/1883": "MQTT",
  "udp/1900": "SSDP",
  "tcp/3074": "Xbox Live",
  "udp/3074": "Xbox Live",
  "tcp/3389": "RDP",
  "udp/3478": "STUN",
  "udp/4500": "IPsec NAT-T",
  "tcp/5222": "XMPP",
  "tcp/5223": "Apple Push",
  "tcp/5228": "Google Play",
  "udp/5353": "mDNS",
  "tcp/8080": "HTTP alt",
  "tcp/8443": "HTTPS alt",
  "tcp/8883": "MQTT TLS",
  "tcp/32400": "Plex",
  "udp/51820": "WireGuard",
}

export function serviceName(protocol: number, port: number): string | null {
  return SERVICES[`${protocolName(protocol).toLowerCase()}/${port}`] ?? null
}

/** "HTTPS · TCP 443", or "TCP 8123" for unknown ports, or "ICMP" for portless protocols. */
export function serviceLabel(protocol: number, port: number): string {
  const proto = protocolName(protocol)
  if (!port) return proto
  const name = serviceName(protocol, port)
  return name ? `${name} · ${proto} ${port}` : `${proto} ${port}`
}

const regionNames =
  typeof Intl.DisplayNames === "function"
    ? new Intl.DisplayNames(undefined, { type: "region" })
    : null

export function countryName(code: string): string {
  try {
    return regionNames?.of(code.toUpperCase()) ?? code
  } catch {
    return code
  }
}

/** "AS15169", the way autonomous systems are written. */
export function asnNumber(number: number | string): string {
  return `AS${number}`
}

/** "AS15169 · GOOGLE", or just the number when the organization isn't known. */
export function asnLabel(number: number | string, organization?: string): string {
  return organization ? `${asnNumber(number)} · ${organization}` : asnNumber(number)
}

export function countryFlag(code: string): string {
  if (!/^[a-z]{2}$/i.test(code)) return ""
  return String.fromCodePoint(
    ...[...code.toUpperCase()].map((char) => 0x1f1e6 + char.charCodeAt(0) - 65)
  )
}
