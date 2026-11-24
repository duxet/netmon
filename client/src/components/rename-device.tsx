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
import { renameDevice, resetDeviceName } from "@/lib/api"
import { deviceLabel, type Device } from "@/lib/network"

const MAX_NAME_LENGTH = 64

type NamedDevice = Pick<Device, "mac" | "name" | "hostname" | "vendor" | "addresses">

function RenameForm({ device, onDone }: { device: NamedDevice; onDone: () => void }) {
  const queryClient = useQueryClient()
  const [name, setName] = React.useState(device.name ?? "")
  const id = React.useId()

  const mutation = useMutation({
    mutationFn: async (next: string) => {
      if (next) await renameDevice(device.mac, next)
      else await resetDeviceName(device.mac)
    },
    onSuccess: async () => {
      // Every client list holds names, whatever its time range.
      await queryClient.invalidateQueries({ queryKey: ["clients"] })
      onDone()
    },
  })

  const trimmed = name.trim()
  const unchanged = trimmed === (device.name ?? "")

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(event) => {
        event.preventDefault()
        if (!unchanged) mutation.mutate(trimmed)
      }}
    >
      <DialogHeader>
        <DialogTitle>Rename device</DialogTitle>
        <DialogDescription>
          The name is shown instead of the hostname everywhere in netmon.
        </DialogDescription>
      </DialogHeader>

      <Field data-invalid={mutation.isError || undefined}>
        <FieldLabel htmlFor={id}>Name</FieldLabel>
        <Input
          id={id}
          value={name}
          onChange={(event) => setName(event.target.value)}
          maxLength={MAX_NAME_LENGTH}
          placeholder={device.hostname ?? "e.g. Living room TV"}
          aria-invalid={mutation.isError || undefined}
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
        {mutation.isError && <FieldError>{mutation.error.message}</FieldError>}
      </Field>

      <DialogFooter>
        <DialogClose asChild>
          <Button type="button" variant="outline">
            Cancel
          </Button>
        </DialogClose>
        <Button type="submit" disabled={unchanged || mutation.isPending}>
          {mutation.isPending && <Loader2Icon data-icon="inline-start" className="animate-spin" />}
          {trimmed || !device.name ? "Save" : "Remove name"}
        </Button>
      </DialogFooter>
    </form>
  )
}

/** A pencil button that opens a dialog to name (or rename) a device. */
export function RenameDeviceButton({ device }: { device: NamedDevice }) {
  const [open, setOpen] = React.useState(false)

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button
          variant="ghost"
          size="icon-sm"
          className="text-muted-foreground hover:text-foreground"
          aria-label={`Rename ${deviceLabel(device)}`}
        >
          <PencilIcon />
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        {/* Mounted only while open, so it starts from the current name each time. */}
        {open && <RenameForm device={device} onDone={() => setOpen(false)} />}
      </DialogContent>
    </Dialog>
  )
}
