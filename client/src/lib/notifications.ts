import type { NotificationService } from "@/lib/api"

/** The scheme of a service URL, as the server reads it ("generic+https" is "generic"). */
export function urlScheme(url: string): string {
  const match = /^([a-z][a-z0-9+.-]*):\/\//i.exec(url.trim())
  return match ? match[1].toLowerCase().split("+")[0] : ""
}

/** The service's name, or its scheme for one netmon doesn't know. */
export function serviceName(services: NotificationService[] | undefined, scheme: string): string {
  return services?.find((service) => service.Scheme === scheme)?.Name ?? scheme
}
