import { useQuery } from "@tanstack/react-query"
import * as React from "react"

import type { Network, NetworkColor } from "@/lib/api"
import { networksQuery } from "@/lib/queries"

/** The value of the clients' network filter for devices in no network. */
export const NO_NETWORK = "none"

export const COLOR_LABELS: Record<NetworkColor, string> = {
  blue: "Blue",
  orange: "Orange",
  aqua: "Aqua",
  yellow: "Yellow",
  magenta: "Magenta",
  green: "Green",
  violet: "Violet",
  red: "Red",
}

/** The CSS color of a network, for its dot; it follows the light or dark theme. */
export function networkColor(color: string): string {
  return `var(--network-${color}, var(--muted-foreground))`
}

/** "VLAN 20 · 192.168.20.0/24, fd00:20::/64" */
export function describeNetwork(network: Pick<Network, "VLAN" | "Prefixes">): string {
  const blocks = network.Prefixes.join(", ")
  return network.VLAN !== null ? `VLAN ${network.VLAN} · ${blocks}` : blocks
}

/** The blocks of addresses typed into a form: one per line, or separated by commas or spaces. */
export function parseBlocks(text: string): string[] {
  return text
    .split(/[\s,;]+/)
    .map((block) => block.trim())
    .filter(Boolean)
}

/** The networks, and a lookup by ID. */
export function useNetworks() {
  const query = useQuery(networksQuery())
  const byId = React.useMemo(
    () => new Map((query.data ?? []).map((network) => [network.ID, network])),
    [query.data]
  )
  return { ...query, list: query.data ?? [], byId }
}
