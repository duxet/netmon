import type { DeviceIconName } from "@/lib/api"
import { deviceIconInfo } from "@/lib/device-icons"
import { cn } from "@/lib/utils"

/**
 * The picture of the kind of device a device is, on a small tile, named for
 * screen readers; the generic picture, which says nothing, when nobody picked
 * one.
 */
export function DeviceIcon({
  icon,
  className,
}: {
  icon: DeviceIconName | null | undefined
  className?: string
}) {
  const { label, Icon } = deviceIconInfo(icon)
  return (
    <span
      className={cn(
        "inline-flex size-8 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground [&_svg]:size-4",
        className
      )}
      {...(icon ? { role: "img", "aria-label": label, title: label } : { "aria-hidden": true })}
    >
      <Icon aria-hidden />
    </span>
  )
}
