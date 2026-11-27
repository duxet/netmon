import { useMutation, useQueryClient } from "@tanstack/react-query"
import { DatabaseIcon, DownloadIcon, UploadIcon } from "lucide-react"
import * as React from "react"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { Label } from "@/components/ui/label"
import { errorText, importSettings, type ImportCount, type ImportResult } from "@/lib/api"

function counted(what: string, count: ImportCount): string | null {
  const parts = []
  if (count.Created) parts.push(`${count.Created} added`)
  if (count.Updated) parts.push(`${count.Updated} updated`)
  return parts.length ? `${what}: ${parts.join(", ")}` : null
}

function summary(result: ImportResult): string {
  const parts = [
    counted("networks", result.Networks),
    counted("devices", result.Devices),
    counted("channels", result.Channels),
    counted("alert rules", result.AlertRules),
  ].filter(Boolean)
  return parts.length ? `Imported ${parts.join("; ")}.` : "There was nothing to import."
}

/** Downloads a backup of the database, and exports and imports the settings. */
export function BackupCard() {
  const queryClient = useQueryClient()
  const [secrets, setSecrets] = React.useState(false)
  const fileInput = React.useRef<HTMLInputElement>(null)
  const secretsId = React.useId()

  const importing = useMutation({
    mutationFn: async (file: File) => importSettings(await file.text()),
    onSuccess: () => void queryClient.invalidateQueries(),
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <DatabaseIcon className="size-4 text-muted-foreground" />
          Backup
        </CardTitle>
        <CardDescription>Copies of what netmon recorded and how it is set up.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-6 text-sm">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div className="flex max-w-prose flex-col gap-1">
            <span className="font-medium">Database</span>
            <p className="text-muted-foreground">
              Everything netmon recorded, with its settings, as a DuckDB file. To restore it, stop netmon, put it in
              place of the database file and remove the <code>.wal</code> file next to it, if there is one.
            </p>
          </div>
          <Button asChild variant="outline" className="shrink-0">
            <a href="/api/backup" download>
              <DownloadIcon data-icon="inline-start" />
              Download backup
            </a>
          </Button>
        </div>

        <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div className="flex max-w-prose flex-col gap-2">
            <span className="font-medium">Settings</span>
            <p className="text-muted-foreground">
              Networks, the names and icons of devices, notification channels and alert rules, as JSON. Importing
              them replaces the networks, channels and rules with the same names, and adds the others.
            </p>
            <div className="flex items-center gap-2">
              <Checkbox id={secretsId} checked={secrets} onCheckedChange={(checked) => setSecrets(checked === true)} />
              <Label htmlFor={secretsId} className="font-normal">
                Include the channels' credentials, such as bot tokens. Keep that file safe.
              </Label>
            </div>
          </div>
          <div className="flex shrink-0 flex-wrap gap-2">
            <Button asChild variant="outline">
              <a href={`/api/settings-export${secrets ? "?secrets=true" : ""}`} download>
                <DownloadIcon data-icon="inline-start" />
                Export
              </a>
            </Button>
            <Button variant="outline" disabled={importing.isPending} onClick={() => fileInput.current?.click()}>
              <UploadIcon data-icon="inline-start" />
              Import…
            </Button>
            <input
              ref={fileInput}
              type="file"
              accept="application/json,.json"
              className="hidden"
              onChange={(event) => {
                const file = event.target.files?.[0]
                if (file) importing.mutate(file)
                event.target.value = ""
              }}
            />
          </div>
        </div>

        {(importing.isSuccess || importing.isError) && (
          <div role="status" className="flex flex-col gap-1">
            {importing.isError ? (
              <p className="break-words text-destructive">{errorText(importing.error)}</p>
            ) : (
              <>
                <p>{summary(importing.data)}</p>
                {importing.data.Warnings.map((warning) => (
                  <p key={warning} className="text-muted-foreground">
                    {warning}
                  </p>
                ))}
              </>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
