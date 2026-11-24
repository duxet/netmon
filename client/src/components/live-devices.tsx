import { Link } from "@tanstack/react-router"

import { Sparkline } from "@/components/charts/sparkline"
import { DeviceVendor } from "@/components/device-vendor"
import { DeviceNetworks } from "@/components/network-badge"
import {
  Table,
  TableBody,
  TableCaption,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { formatBitrate } from "@/lib/format"
import type { LiveDeviceSeries } from "@/lib/live"
import { deviceLabel, type Device } from "@/lib/network"
import { cn } from "@/lib/utils"

function RateCell({ value, active }: { value: number; active: boolean }) {
  return (
    <TableCell className="text-right tabular-nums">
      {active && value > 0 ? formatBitrate(value, 1) : <span className="text-muted-foreground">—</span>}
    </TableCell>
  )
}

function SeriesMarker({ color }: { color: string }) {
  return (
    <span
      aria-hidden
      className="mr-1.5 inline-block size-2 rounded-[2px]"
      style={{ backgroundColor: color }}
    />
  )
}

/**
 * The devices that used the network in the last couple of minutes, busiest
 * first. Devices that went quiet stay listed, dimmed, until they leave the
 * window, so rows don't vanish the moment a download finishes.
 */
export function LiveDevices({
  devices,
  newest,
  known,
}: {
  devices: LiveDeviceSeries[]
  newest: number
  /** What is known about the devices from earlier traffic, by MAC address. */
  known: Map<string, Device>
}) {
  return (
    <Table>
      <TableCaption className="sr-only">
        Current bandwidth per device, busiest first. Bit rates are over the last second.
      </TableCaption>
      <TableHeader>
        <TableRow>
          <TableHead>Device</TableHead>
          <TableHead className="text-right">
            <SeriesMarker color="var(--chart-1)" />
            Download
          </TableHead>
          <TableHead className="text-right">
            <SeriesMarker color="var(--chart-2)" />
            Upload
          </TableHead>
          <TableHead className="hidden w-40 sm:table-cell">Last 2 minutes</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {devices.map((device) => {
          const info = known.get(device.mac)
          const label = info ? deviceLabel(info) : (device.ip ?? device.mac)

          return (
            <TableRow key={device.mac} className={cn(!device.active && "text-muted-foreground")}>
              <TableCell>
                <div className="flex min-w-0 flex-col">
                  <Link
                    to="/clients/$clientId"
                    params={{ clientId: device.mac }}
                    className={cn(
                      "truncate font-medium hover:underline",
                      device.active && "text-foreground"
                    )}
                  >
                    {label}
                  </Link>
                  <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
                    {device.ip && label !== device.ip && <span className="font-mono">{device.ip}</span>}
                    <DeviceVendor device={info ?? { mac: device.mac, vendor: null }} />
                    {info && <DeviceNetworks ids={info.networks} />}
                  </span>
                </div>
              </TableCell>
              <RateCell value={device.download} active={device.active} />
              <RateCell value={device.upload} active={device.active} />
              <TableCell className="hidden sm:table-cell">
                <Sparkline points={device.points} newest={newest} peak={device.peak} />
              </TableCell>
            </TableRow>
          )
        })}
      </TableBody>
    </Table>
  )
}
