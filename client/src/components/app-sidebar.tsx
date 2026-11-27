import { useQuery } from "@tanstack/react-query"
import { Link, useRouterState } from "@tanstack/react-router"
import {
  BellIcon,
  LayoutDashboardIcon,
  ListChecksIcon,
  MonitorSmartphoneIcon,
  NetworkIcon,
  RadarIcon,
  RadioIcon,
  SlidersHorizontalIcon,
  TriangleAlertIcon,
} from "lucide-react"
import type * as React from "react"

import { NavUser } from "@/components/nav-user"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSkeleton,
  SidebarRail,
  useSidebar,
} from "@/components/ui/sidebar"
import { useRange } from "@/hooks/use-range"
import { deviceIconInfo } from "@/lib/device-icons"
import { formatBytes } from "@/lib/format"
import { deviceLabel } from "@/lib/network"
import { alertsQuery, devicesQuery, networksQuery, statusQuery } from "@/lib/queries"

export function AppSidebar(props: React.ComponentProps<typeof Sidebar>) {
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const range = useRange()
  const { data: devices, isPending } = useQuery(devicesQuery(range))
  // The same query as the Alerts page's, so they share one request.
  const { data: firing } = useQuery(alertsQuery({ state: "firing", limit: 100 }))
  const { data: networks } = useQuery(networksQuery())
  const { data: status } = useQuery(statusQuery())
  const { isMobile, setOpenMobile } = useSidebar()
  const closeOnMobile = () => isMobile && setOpenMobile(false)

  const nav = [
    { to: "/", label: "Dashboard", icon: LayoutDashboardIcon, active: pathname === "/" },
    { to: "/live", label: "Live", icon: RadioIcon, active: pathname === "/live" },
    {
      to: "/clients",
      label: "Clients",
      icon: MonitorSmartphoneIcon,
      active: pathname.startsWith("/clients"),
      badge: devices?.length,
    },
    {
      to: "/networks",
      label: "Networks",
      icon: NetworkIcon,
      active: pathname.startsWith("/networks"),
      badge: networks?.length || undefined,
    },
    {
      to: "/alerts",
      label: "Alerts",
      icon: TriangleAlertIcon,
      active: pathname.startsWith("/alerts"),
      badge: firing?.total || undefined,
    },
  ] as const

  const settings = [
    { to: "/settings/alert-rules", label: "Alert rules", icon: ListChecksIcon },
    { to: "/settings/notifications", label: "Notifications", icon: BellIcon },
    { to: "/settings/configuration", label: "Configuration", icon: SlidersHorizontalIcon },
  ] as const

  return (
    <Sidebar collapsible="icon" variant="inset" {...props}>
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size="lg" asChild>
              <Link to="/" onClick={closeOnMobile}>
                <div className="flex aspect-square size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
                  <RadarIcon className="size-4" />
                </div>
                <div className="grid flex-1 text-left leading-tight">
                  <span className="truncate font-semibold">netmon</span>
                  <span className="truncate text-xs text-muted-foreground">
                    Home network monitor
                  </span>
                </div>
              </Link>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>

      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupLabel>Monitor</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              {nav.map((item) => (
                <SidebarMenuItem key={item.to}>
                  <SidebarMenuButton asChild isActive={item.active} tooltip={item.label}>
                    <Link to={item.to} onClick={closeOnMobile}>
                      <item.icon />
                      <span>{item.label}</span>
                    </Link>
                  </SidebarMenuButton>
                  {"badge" in item && item.badge !== undefined && (
                    <SidebarMenuBadge>{item.badge}</SidebarMenuBadge>
                  )}
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>

        <SidebarGroup>
          <SidebarGroupLabel>Settings</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              {settings.map((item) => (
                <SidebarMenuItem key={item.to}>
                  <SidebarMenuButton asChild isActive={pathname === item.to} tooltip={item.label}>
                    <Link to={item.to} onClick={closeOnMobile}>
                      <item.icon />
                      <span>{item.label}</span>
                    </Link>
                  </SidebarMenuButton>
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>

        <SidebarGroup className="group-data-[collapsible=icon]:hidden">
          <SidebarGroupLabel>Top clients</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              {isPending &&
                Array.from({ length: 4 }, (_, index) => (
                  <SidebarMenuItem key={index}>
                    <SidebarMenuSkeleton />
                  </SidebarMenuItem>
                ))}
              {devices?.slice(0, 5).map((device) => {
                const href = `/clients/${device.mac}`
                const { Icon } = deviceIconInfo(device.icon)
                return (
                  <SidebarMenuItem key={device.mac}>
                    <SidebarMenuButton asChild size="sm" isActive={pathname === href}>
                      <Link
                        to="/clients/$clientId"
                        params={{ clientId: device.mac }}
                        onClick={closeOnMobile}
                      >
                        <Icon aria-hidden className={device.icon ? undefined : "text-muted-foreground"} />
                        <span className="truncate">{deviceLabel(device)}</span>
                      </Link>
                    </SidebarMenuButton>
                    <SidebarMenuBadge className="font-normal text-muted-foreground tabular-nums">
                      {formatBytes(device.total, 0)}
                    </SidebarMenuBadge>
                  </SidebarMenuItem>
                )
              })}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter>
        <NavUser />
        {status && (
          <p
            className="truncate px-2 text-xs text-muted-foreground group-data-[collapsible=icon]:hidden"
            title={`netmon ${status.Version}`}
          >
            netmon {status.Version}
          </p>
        )}
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}
