import { Link } from "@tanstack/react-router"

import { Badge } from "@/components/ui/badge"
import type { Network } from "@/lib/api"
import { describeNetwork, networkColor, useNetworks } from "@/lib/networks"
import { cn } from "@/lib/utils"

/** A network's color, as a small square beside its name. */
export function NetworkDot({ color, className }: { color: string; className?: string }) {
  return (
    <span
      aria-hidden
      className={cn("inline-block size-2 shrink-0 rounded-[2px]", className)}
      style={{ backgroundColor: networkColor(color) }}
    />
  )
}

/** A network's name with its color, linking to its page. */
export function NetworkBadge({ network, className }: { network: Network; className?: string }) {
  return (
    <Badge variant="outline" className={cn("font-normal", className)} title={describeNetwork(network)} asChild>
      <Link to="/networks/$networkId" params={{ networkId: String(network.ID) }}>
        <NetworkDot color={network.Color} />
        {network.Name}
      </Link>
    </Badge>
  )
}

/** Badges of the networks a device's addresses belong to; nothing for none. */
export function DeviceNetworks({ ids, className }: { ids: number[]; className?: string }) {
  const { byId } = useNetworks()
  const networks = ids.flatMap((id) => byId.get(id) ?? [])
  if (networks.length === 0) return null
  return (
    <span className={cn("inline-flex flex-wrap gap-1", className)}>
      {networks.map((network) => (
        <NetworkBadge key={network.ID} network={network} />
      ))}
    </span>
  )
}

/** A network's name, for the breadcrumb, while the networks load too. */
export function NetworkName({ id }: { id: string }) {
  const { byId } = useNetworks()
  return byId.get(Number(id))?.Name ?? "Network"
}
