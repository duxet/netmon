import { createFileRoute, Outlet } from "@tanstack/react-router"

// Layout for /alerts and /alerts/$alertId, so both get an "Alerts" crumb.
export const Route = createFileRoute("/alerts")({
  staticData: { breadcrumb: "Alerts" },
  component: Outlet,
})
