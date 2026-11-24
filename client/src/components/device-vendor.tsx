import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { isRandomizedMac, type Device } from "@/lib/network"
import { cn } from "@/lib/utils"

/**
 * The manufacturer of a device's network interface, as a small detail; for
 * a randomized MAC address, says so instead. Renders nothing when unknown.
 */
export function DeviceVendor({
  device,
  className,
}: {
  device: Pick<Device, "mac" | "vendor">
  className?: string
}) {
  if (device.vendor) {
    return (
      <span className={className} title="Manufacturer, from the MAC address">
        {device.vendor}
      </span>
    )
  }

  if (!isRandomizedMac(device.mac)) return null

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className={cn("cursor-help underline decoration-dotted underline-offset-2", className)} tabIndex={0}>
          Private MAC address
        </span>
      </TooltipTrigger>
      <TooltipContent className="max-w-64">
        This device uses a randomized address, as phones and laptops do for privacy, so its
        manufacturer isn't known.
      </TooltipContent>
    </Tooltip>
  )
}
