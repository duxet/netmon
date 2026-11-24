import { createFileRoute, Outlet } from "@tanstack/react-router"

// Layout for /clients and /clients/$clientId, so both get a "Clients" crumb.
export const Route = createFileRoute("/clients")({
  staticData: { breadcrumb: "Clients" },
  component: Outlet,
})
