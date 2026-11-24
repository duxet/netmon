import { useMutation, useQuery } from "@tanstack/react-query"
import { ChevronsUpDownIcon, LogOutIcon } from "lucide-react"

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from "@/components/ui/sidebar"
import { errorText, signOut } from "@/lib/api"
import { sessionQuery } from "@/lib/queries"

function initials(label: string) {
  const parts = label.split(/[\s@._-]+/).filter(Boolean)
  const letters = parts.length > 1 ? parts[0][0] + parts[1][0] : label.slice(0, 2)
  return letters.toUpperCase()
}

/**
 * Who is signed in, with a way to sign out. Shows nothing when netmon has no
 * sign-in, as there is nobody to show.
 */
export function NavUser() {
  const { data: session } = useQuery(sessionQuery())
  const { isMobile } = useSidebar()
  const signingOut = useMutation({ mutationFn: signOut })

  if (!session?.AuthEnabled || !session.User) return null

  const { Name: name, Email: email } = session.User
  const label = name ?? email ?? "Signed in"

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <SidebarMenuButton
              size="lg"
              tooltip={label}
              className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground"
            >
              <div className="flex aspect-square size-8 items-center justify-center rounded-lg bg-muted text-xs font-medium text-muted-foreground">
                {initials(label)}
              </div>
              <div className="grid flex-1 text-left leading-tight">
                <span className="truncate font-medium">{label}</span>
                {name && email && (
                  <span className="truncate text-xs text-muted-foreground">{email}</span>
                )}
              </div>
              <ChevronsUpDownIcon className="ml-auto size-4" />
            </SidebarMenuButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            side={isMobile ? "bottom" : "right"}
            align="end"
            sideOffset={4}
            className="min-w-56"
          >
            <DropdownMenuLabel className="font-normal">
              <div className="grid leading-tight">
                <span className="truncate font-medium">{label}</span>
                {name && email && (
                  <span className="truncate text-xs text-muted-foreground">{email}</span>
                )}
              </div>
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
            {/* Keep the menu open: on success the page leaves, on failure it says why. */}
            <DropdownMenuItem
              disabled={signingOut.isPending}
              onSelect={(event) => {
                event.preventDefault()
                signingOut.mutate()
              }}
            >
              <LogOutIcon />
              Sign out
            </DropdownMenuItem>
            {signingOut.isError && (
              <p role="alert" className="px-2 py-1.5 text-xs text-destructive">
                Could not sign out: {errorText(signingOut.error)}
              </p>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarMenuItem>
    </SidebarMenu>
  )
}
