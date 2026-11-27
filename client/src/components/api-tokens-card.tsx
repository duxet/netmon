import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { CheckIcon, CopyIcon, KeyRoundIcon, PlusIcon, Trash2Icon } from "lucide-react"
import * as React from "react"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { createToken, deleteToken, errorText, type ApiToken, type NewApiToken } from "@/lib/api"
import { formatDayTime } from "@/lib/format"
import { sessionQuery, tokensQuery } from "@/lib/queries"

/** Makes, lists and revokes the read-only API tokens. */
export function ApiTokensCard() {
  const queryClient = useQueryClient()
  const tokens = useQuery(tokensQuery())
  const { data: session } = useQuery(sessionQuery())
  const [name, setName] = React.useState("")
  const [made, setMade] = React.useState<NewApiToken | null>(null)
  const [copied, setCopied] = React.useState(false)
  const [revoking, setRevoking] = React.useState<ApiToken | null>(null)

  const create = useMutation({
    mutationFn: () => createToken(name.trim()),
    onSuccess: (token) => {
      setMade(token)
      setCopied(false)
      setName("")
      void queryClient.invalidateQueries({ queryKey: tokensQuery().queryKey })
    },
  })
  const revoke = useMutation({
    mutationFn: (id: number) => deleteToken(id),
    onSuccess: () => {
      setRevoking(null)
      void queryClient.invalidateQueries({ queryKey: tokensQuery().queryKey })
    },
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <KeyRoundIcon className="size-4 text-muted-foreground" />
          API tokens
        </CardTitle>
        <CardDescription>
          {session?.AuthEnabled
            ? "Scripts, Prometheus and Home Assistant can't sign in, so they send a token instead: "
            : "Without single sign-on, the API needs no token; these only count once it is on. "}
          <code>Authorization: Bearer netmon_…</code>. Tokens can read traffic and settings, but not change
          anything, download backups or read credentials.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4 text-sm">
        <form
          className="flex flex-col gap-2 sm:flex-row"
          onSubmit={(event) => {
            event.preventDefault()
            if (name.trim()) create.mutate()
          }}
        >
          <Input
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="What it is for, such as Grafana"
            aria-label="Token name"
            className="sm:max-w-80"
            maxLength={64}
          />
          <Button type="submit" variant="outline" disabled={!name.trim() || create.isPending}>
            <PlusIcon data-icon="inline-start" />
            Make a token
          </Button>
        </form>
        {create.isError && <p className="text-destructive">{errorText(create.error)}</p>}

        {made && (
          <div className="flex flex-col gap-2 rounded-lg border border-chart-3/50 bg-chart-3/5 p-3" role="status">
            <p>
              <span className="font-medium">The token for {made.Name}.</span> Copy it now: it isn't shown again.
            </p>
            <div className="flex items-center gap-2">
              <code className="min-w-0 flex-1 break-all rounded bg-muted px-2 py-1 font-mono text-xs">{made.Token}</code>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label="Copy the token"
                onClick={() => {
                  void navigator.clipboard?.writeText(made.Token).then(() => setCopied(true))
                }}
              >
                {copied ? <CheckIcon /> : <CopyIcon />}
              </Button>
            </div>
          </div>
        )}

        {tokens.isError ? (
          <p className="text-destructive">{errorText(tokens.error)}</p>
        ) : tokens.data && tokens.data.length > 0 ? (
          <ul className="divide-y">
            {tokens.data.map((token) => (
              <li key={token.ID} className="flex items-center gap-3 py-2">
                <div className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate font-medium">{token.Name}</span>
                  <span className="truncate text-xs text-muted-foreground">
                    <code>{token.Prefix}…</code> · made by {token.CreatedBy} on{" "}
                    {formatDayTime(new Date(token.CreatedAt))} ·{" "}
                    {token.LastUsedAt ? `last used ${formatDayTime(new Date(token.LastUsedAt))}` : "never used"}
                  </span>
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  className="text-muted-foreground hover:text-destructive"
                  aria-label={`Revoke ${token.Name}`}
                  onClick={() => setRevoking(token)}
                >
                  <Trash2Icon />
                </Button>
              </li>
            ))}
          </ul>
        ) : (
          tokens.data && <p className="text-muted-foreground">No tokens yet.</p>
        )}
      </CardContent>

      <AlertDialog open={revoking !== null} onOpenChange={(open) => !open && setRevoking(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Revoke {revoking?.Name}?</AlertDialogTitle>
            <AlertDialogDescription>
              Whatever uses it can't reach the API any more, right away. This can't be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          {revoke.isError && <p className="text-sm text-destructive">{errorText(revoke.error)}</p>}
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={(event) => {
                event.preventDefault()
                if (revoking) revoke.mutate(revoking.ID)
              }}
            >
              Revoke
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  )
}
