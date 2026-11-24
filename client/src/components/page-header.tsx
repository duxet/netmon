import type * as React from "react"

export function PageHeader({
  title,
  description,
  children,
}: {
  title: React.ReactNode
  description?: React.ReactNode
  /** Controls aligned to the right of the title, e.g. filters. */
  children?: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
      <div className="flex min-w-0 flex-col gap-1">
        <h1 className="font-heading text-xl font-semibold tracking-tight">{title}</h1>
        {description && <div className="text-sm text-muted-foreground">{description}</div>}
      </div>
      {children && <div className="flex shrink-0 items-center gap-2">{children}</div>}
    </div>
  )
}

export function Page({ children }: { children: React.ReactNode }) {
  return (
    <div className="mx-auto flex w-full max-w-7xl flex-col gap-4 p-4 lg:gap-6 lg:p-6">
      {children}
    </div>
  )
}
