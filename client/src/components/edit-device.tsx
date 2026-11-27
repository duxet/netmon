import { useMutation, useQueryClient } from "@tanstack/react-query"
import { Loader2Icon, PencilIcon } from "lucide-react"
import * as React from "react"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  DEVICE_ICONS,
  renameDevice,
  resetDeviceIcon,
  resetDeviceName,
  setDeviceIcon,
  type DeviceIconName,
} from "@/lib/api"
import { DEVICE_ICON_INFO, GENERIC_DEVICE_ICON } from "@/lib/device-icons"
import { deviceLabel, type Device } from "@/lib/network"

const MAX_NAME_LENGTH = 64

/** The picker's value for no icon, which the generic picture stands for. */
const NO_ICON = "generic"

type EditableDevice = Pick<Device, "mac" | "name" | "icon" | "hostname" | "vendor" | "addresses">

/** Picks one of the icons, or none. */
function IconPicker({
  value,
  onChange,
  labelId,
}: {
  value: DeviceIconName | null
  onChange: (icon: DeviceIconName | null) => void
  labelId: string
}) {
  const options = [
    { value: NO_ICON, ...GENERIC_DEVICE_ICON, label: "None" },
    ...DEVICE_ICONS.map((icon) => ({ value: icon, ...DEVICE_ICON_INFO[icon] })),
  ]

  return (
    <ToggleGroup
      type="single"
      variant="outline"
      spacing={1.5}
      value={value ?? NO_ICON}
      // Clicking the picked icon again would pick nothing; keep it instead.
      onValueChange={(next) => next && onChange(next === NO_ICON ? null : (next as DeviceIconName))}
      aria-labelledby={labelId}
      className="grid w-full grid-cols-6 sm:grid-cols-8"
    >
      {options.map(({ value: option, label, Icon }) => (
        <ToggleGroupItem
          key={option}
          value={option}
          aria-label={label}
          title={label}
          className="h-9 w-full data-[state=on]:z-10 data-[state=on]:border-foreground data-[state=on]:ring-1 data-[state=on]:ring-foreground [&_svg:not([class*='size-'])]:size-4.5"
        >
          <Icon aria-hidden />
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  )
}

function EditForm({ device, onDone }: { device: EditableDevice; onDone: () => void }) {
  const queryClient = useQueryClient()
  const [name, setName] = React.useState(device.name ?? "")
  const [icon, setIcon] = React.useState<DeviceIconName | null>(device.icon)
  const id = React.useId()

  const trimmed = name.trim()
  const nameChanged = trimmed !== (device.name ?? "")
  const iconChanged = icon !== device.icon

  const mutation = useMutation({
    // Only what changed is saved, so a retry after one of the two failed
    // doesn't repeat the other.
    mutationFn: async () => {
      if (nameChanged) {
        if (trimmed) await renameDevice(device.mac, trimmed)
        else await resetDeviceName(device.mac)
      }
      if (iconChanged) {
        if (icon) await setDeviceIcon(device.mac, icon)
        else await resetDeviceIcon(device.mac)
      }
    },
    // Even after a failure, as the name may have been saved before the icon
    // failed. Every client list holds names and icons, whatever its time
    // range, and so does the list of every device.
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: ["clients"] }),
        queryClient.invalidateQueries({ queryKey: ["known-devices"] }),
      ]),
    onSuccess: onDone,
  })

  const unchanged = !nameChanged && !iconChanged
  // The name is saved first: once it is, the refetched device has it, so a
  // name still unsaved after a failure is what failed.
  const nameInvalid = (mutation.isError && nameChanged) || undefined
  const picked = icon ? DEVICE_ICON_INFO[icon].label : null

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(event) => {
        event.preventDefault()
        if (!unchanged) mutation.mutate()
      }}
    >
      <DialogHeader>
        <DialogTitle>Edit device</DialogTitle>
        <DialogDescription>
          The name is shown instead of the hostname everywhere in netmon, and the icon next to it.
        </DialogDescription>
      </DialogHeader>

      <Field data-invalid={nameInvalid}>
        <FieldLabel htmlFor={`${id}-name`}>Name</FieldLabel>
        <Input
          id={`${id}-name`}
          value={name}
          onChange={(event) => setName(event.target.value)}
          maxLength={MAX_NAME_LENGTH}
          placeholder={device.hostname ?? "e.g. Living room TV"}
          aria-invalid={nameInvalid}
          autoComplete="off"
          autoFocus
        />
        <FieldDescription>
          <span className="font-mono">{device.mac}</span>
          {device.vendor && ` (${device.vendor})`}
          {" · "}
          {device.hostname
            ? `Leave empty to use its hostname, ${device.hostname}.`
            : "Leave empty to remove the name."}
        </FieldDescription>
      </Field>

      <Field>
        <FieldLabel id={`${id}-icon`}>Icon</FieldLabel>
        <IconPicker value={icon} onChange={setIcon} labelId={`${id}-icon`} />
        <FieldDescription aria-live="polite">
          {picked ?? "No icon: the device is shown with a generic one."}
        </FieldDescription>
      </Field>

      {mutation.isError && <FieldError>{mutation.error.message}</FieldError>}

      <DialogFooter>
        <DialogClose asChild>
          <Button type="button" variant="outline">
            Cancel
          </Button>
        </DialogClose>
        <Button type="submit" disabled={unchanged || mutation.isPending}>
          {mutation.isPending && <Loader2Icon data-icon="inline-start" className="animate-spin" />}
          {!iconChanged && !trimmed && device.name ? "Remove name" : "Save"}
        </Button>
      </DialogFooter>
    </form>
  )
}

/** A pencil button that opens a dialog to name a device and pick its icon. */
export function EditDeviceButton({ device }: { device: EditableDevice }) {
  const [open, setOpen] = React.useState(false)

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button
          variant="ghost"
          size="icon-sm"
          className="text-muted-foreground hover:text-foreground"
          aria-label={`Edit ${deviceLabel(device)}`}
        >
          <PencilIcon />
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        {/* Mounted only while open, so it starts from the current name and icon each time. */}
        {open && <EditForm device={device} onDone={() => setOpen(false)} />}
      </DialogContent>
    </Dialog>
  )
}
