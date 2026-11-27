import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { MergeIcon, Trash2Icon, UndoIcon } from "lucide-react"
import * as React from "react"

import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { errorText, forgetDevice, mergeDevices, unmergeAddress, type KnownDevice } from "@/lib/api"
import { knownDevicesQuery } from "@/lib/queries"

function label(device: KnownDevice): string {
  const name = device.Name ?? device.Hostname?.replace(/\.$/, "") ?? device.IPAddress
  return `${name} (${device.MACAddress})`
}

/**
 * Merges other MAC addresses into a device, such as the random ones a phone
 * uses per network, and forgets it.
 */
export function ManageDevice({ mac, onForgotten }: { mac: string; onForgotten: () => void }) {
  const queryClient = useQueryClient()
  const known = useQuery(knownDevicesQuery())
  const [merging, setMerging] = React.useState("")
  const [confirmMerge, setConfirmMerge] = React.useState(false)
  const [confirmForget, setConfirmForget] = React.useState(false)
  const [withTraffic, setWithTraffic] = React.useState(false)
  const id = React.useId()

  const device = known.data?.find((d) => d.MACAddress === mac)
  const others = (known.data ?? []).filter((d) => d.MACAddress !== mac)
  const refresh = () => queryClient.invalidateQueries()

  const merge = useMutation({
    mutationFn: () => mergeDevices(mac, [merging]),
    onSuccess: () => {
      setMerging("")
      setConfirmMerge(false)
    },
    onSettled: refresh,
  })
  const unmerge = useMutation({ mutationFn: (alias: string) => unmergeAddress(mac, alias), onSettled: refresh })
  const forget = useMutation({
    mutationFn: () => forgetDevice(mac, withTraffic),
    onSuccess: () => {
      onForgotten()
      void refresh()
    },
  })

  return (
    <div className="flex flex-col gap-4 border-t pt-4">
      <Field>
        <FieldLabel htmlFor={`${id}-merge`}>Merge another address into it</FieldLabel>
        <div className="flex gap-2">
          <Select
            value={merging}
            onValueChange={(value) => {
              setMerging(value)
              setConfirmMerge(false)
            }}
          >
            <SelectTrigger id={`${id}-merge`} className="min-w-0 flex-1">
              <SelectValue placeholder="Pick a device" />
            </SelectTrigger>
            <SelectContent position="popper">
              {others.map((other) => (
                <SelectItem key={other.MACAddress} value={other.MACAddress}>
                  {label(other)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button
            type="button"
            variant={confirmMerge ? "default" : "outline"}
            disabled={!merging || merge.isPending}
            onClick={() => (confirmMerge ? merge.mutate() : setConfirmMerge(true))}
          >
            <MergeIcon data-icon="inline-start" />
            {confirmMerge ? "Merge for good" : "Merge"}
          </Button>
        </div>
        <FieldDescription>
          {confirmMerge
            ? "Its traffic so far becomes this device's, which can't be undone, and so does its traffic to come."
            : "For a phone that uses a random address on each network, say: its traffic, past and to come, counts as this device's."}
        </FieldDescription>
        {merge.isError && <FieldError>{errorText(merge.error)}</FieldError>}
      </Field>

      {device && device.Aliases.length > 0 && (
        <div className="flex flex-col gap-1 text-sm">
          <span className="font-medium">Merged addresses</span>
          {device.Aliases.map((alias) => (
            <div key={alias} className="flex items-center justify-between gap-2">
              <code className="font-mono text-xs">{alias}</code>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                disabled={unmerge.isPending}
                onClick={() => unmerge.mutate(alias)}
                title="Its traffic to come counts as its own again; what was merged stays this device's."
              >
                <UndoIcon data-icon="inline-start" />
                Unmerge
              </Button>
            </div>
          ))}
          {unmerge.isError && <FieldError>{errorText(unmerge.error)}</FieldError>}
        </div>
      )}

      <div className="flex flex-col gap-2">
        {confirmForget && (
          <div className="flex items-center gap-2">
            <Checkbox id={`${id}-traffic`} checked={withTraffic} onCheckedChange={(c) => setWithTraffic(c === true)} />
            <Label htmlFor={`${id}-traffic`} className="font-normal">
              Delete its traffic too
            </Label>
          </div>
        )}
        <div className="flex items-center gap-2">
          <Button
            type="button"
            variant={confirmForget ? "destructive" : "outline"}
            disabled={forget.isPending}
            onClick={() => (confirmForget ? forget.mutate() : setConfirmForget(true))}
          >
            <Trash2Icon data-icon="inline-start" />
            {confirmForget ? "Forget for good" : "Forget device"}
          </Button>
          {confirmForget && (
            <Button type="button" variant="ghost" onClick={() => setConfirmForget(false)}>
              Keep it
            </Button>
          )}
        </div>
        <p className="text-sm text-muted-foreground">
          {confirmForget
            ? withTraffic
              ? "Its name, icon and history are deleted. It is a new device if it comes back."
              : "Its name and icon are deleted, its traffic stays. It is a new device if it comes back."
            : "Removes a device that is gone for good from the list of devices."}
        </p>
        {forget.isError && <FieldError>{errorText(forget.error)}</FieldError>}
      </div>
    </div>
  )
}
