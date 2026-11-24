import { useMutation, useQueryClient } from "@tanstack/react-query"
import { Loader2Icon } from "lucide-react"
import * as React from "react"

import { NetworkDot } from "@/components/network-badge"
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
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  createNetwork,
  errorText,
  NETWORK_COLORS,
  updateNetwork,
  type Network,
  type NetworkColor,
} from "@/lib/api"
import { COLOR_LABELS, parseBlocks, useNetworks } from "@/lib/networks"

const MAX_NAME_LENGTH = 64
const MIN_VLAN = 1
const MAX_VLAN = 4094

/** What a new network's form starts with, such as a suggested block. */
export interface NetworkPreset {
  name?: string
  prefixes?: string[]
}

function NetworkForm({
  network,
  preset,
  onDone,
}: {
  /** The network to edit, or undefined to add one. */
  network?: Network
  preset?: NetworkPreset
  onDone: (saved: Network) => void
}) {
  const queryClient = useQueryClient()
  const { list } = useNetworks()
  const nameId = React.useId()
  const blocksId = React.useId()
  const vlanId = React.useId()

  // A new network starts with a color no other network has, as the server
  // would pick, so that it looks different from the others.
  const firstFree = NETWORK_COLORS.find((color) => !list.some((n) => n.Color === color)) ?? NETWORK_COLORS[0]

  const [name, setName] = React.useState(network?.Name ?? preset?.name ?? "")
  const [blocks, setBlocks] = React.useState((network?.Prefixes ?? preset?.prefixes ?? []).join("\n"))
  const [vlan, setVLAN] = React.useState(network?.VLAN?.toString() ?? "")
  const [color, setColor] = React.useState<NetworkColor>(network?.Color ?? firstFree)

  const trimmedName = name.trim()
  const prefixes = parseBlocks(blocks)
  const vlanNumber = vlan.trim() === "" ? null : Number(vlan)
  const vlanValid =
    vlanNumber === null || (Number.isInteger(vlanNumber) && vlanNumber >= MIN_VLAN && vlanNumber <= MAX_VLAN)
  const canSave = trimmedName !== "" && prefixes.length > 0 && vlanValid

  const save = useMutation({
    mutationFn: () => {
      const input = { name: trimmedName, vlan: vlanNumber, color, prefixes }
      return network ? updateNetwork(network.ID, input) : createNetwork(input)
    },
    onSuccess: async (saved) => {
      // Which network a device is in shows up nearly everywhere.
      await queryClient.invalidateQueries()
      onDone(saved)
    },
  })

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(event) => {
        event.preventDefault()
        if (canSave) save.mutate()
      }}
    >
      <DialogHeader>
        <DialogTitle>{network ? "Edit network" : "Add network"}</DialogTitle>
        <DialogDescription>
          Group devices by the addresses they use, such as a VLAN or a subnet, to tell them apart and
          see each group's traffic.
        </DialogDescription>
      </DialogHeader>

      <FieldGroup>
        <Field>
          <FieldLabel htmlFor={nameId}>Name</FieldLabel>
          <Input
            id={nameId}
            value={name}
            onChange={(event) => setName(event.target.value)}
            maxLength={MAX_NAME_LENGTH}
            placeholder="e.g. IoT, Guests or Home"
            autoComplete="off"
            autoFocus
          />
        </Field>

        <Field>
          <FieldLabel htmlFor={blocksId}>Address blocks</FieldLabel>
          <Textarea
            id={blocksId}
            value={blocks}
            onChange={(event) => setBlocks(event.target.value)}
            placeholder={"192.168.20.0/24\nfd00:20::/64"}
            className="min-h-20 font-mono"
            autoComplete="off"
            spellCheck={false}
          />
          <FieldDescription>
            One per line, in CIDR notation, or single addresses. A device is in the network with the
            most specific block that holds its address, so a block can sit inside another network's.
          </FieldDescription>
        </Field>

        <Field data-invalid={!vlanValid || undefined}>
          <FieldLabel htmlFor={vlanId}>VLAN ID (optional)</FieldLabel>
          {/* Fields stretch their children, so cap the width rather than set it. */}
          <Input
            id={vlanId}
            value={vlan}
            onChange={(event) => setVLAN(event.target.value)}
            inputMode="numeric"
            placeholder="e.g. 20"
            className="max-w-32"
            autoComplete="off"
            aria-invalid={!vlanValid || undefined}
          />
          {vlanValid ? (
            <FieldDescription>Shown for reference: devices are matched by their address.</FieldDescription>
          ) : (
            <FieldError>
              A VLAN ID is a number from {MIN_VLAN} to {MAX_VLAN}.
            </FieldError>
          )}
        </Field>

        <Field>
          <FieldLabel>Color</FieldLabel>
          <ToggleGroup
            type="single"
            variant="outline"
            size="sm"
            value={color}
            onValueChange={(value) => value && setColor(value as NetworkColor)}
            aria-label="Color"
            className="flex-wrap"
          >
            {NETWORK_COLORS.map((c) => (
              <ToggleGroupItem
                key={c}
                value={c}
                aria-label={COLOR_LABELS[c]}
                title={COLOR_LABELS[c]}
                className="px-2.5 data-[state=on]:z-10 data-[state=on]:border-foreground data-[state=on]:ring-1 data-[state=on]:ring-foreground"
              >
                <NetworkDot color={c} className="size-3 rounded-full" />
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </Field>
      </FieldGroup>

      {save.isError && <FieldError>{errorText(save.error)}</FieldError>}

      <DialogFooter>
        <DialogClose asChild>
          <Button type="button" variant="outline">
            Cancel
          </Button>
        </DialogClose>
        <Button type="submit" disabled={!canSave || save.isPending}>
          {save.isPending && <Loader2Icon data-icon="inline-start" className="animate-spin" />}
          {network ? "Save" : "Add network"}
        </Button>
      </DialogFooter>
    </form>
  )
}

/** A dialog to add a network, or edit one, opened by its trigger. */
export function NetworkDialog({
  network,
  preset,
  onSaved,
  children,
}: {
  network?: Network
  preset?: NetworkPreset
  onSaved?: (network: Network) => void
  /** The button that opens the dialog. */
  children: React.ReactNode
}) {
  const [open, setOpen] = React.useState(false)

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{children}</DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        {/* Mounted only while open, so the form starts afresh each time. */}
        {open && (
          <NetworkForm
            network={network}
            preset={preset}
            onDone={(saved) => {
              setOpen(false)
              onSaved?.(saved)
            }}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}
