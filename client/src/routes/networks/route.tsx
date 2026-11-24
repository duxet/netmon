import { createFileRoute, Outlet } from "@tanstack/react-router"

// Layout for /networks and /networks/$networkId, so both get a "Networks" crumb.
export const Route = createFileRoute("/networks")({
  staticData: { breadcrumb: "Networks" },
  component: Outlet,
})
