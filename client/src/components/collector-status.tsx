import { useQuery } from "@tanstack/react-query"
import { TriangleAlertIcon } from "lucide-react"

import { Card, CardContent } from "@/components/ui/card"
import { collectorProblems } from "@/lib/collector"
import { statusQuery } from "@/lib/queries"

/**
 * Says so when netmon isn't recording traffic, or not all of it, so that an
 * empty or patchy dashboard doesn't look like a quiet network.
 */
export function CollectorStatus() {
  const { data: status } = useQuery(statusQuery())
  const problems = collectorProblems(status?.Collector ?? null)
  if (problems.length === 0) return null

  return (
    <div className="mx-auto w-full max-w-7xl px-4 pt-4 lg:px-6 lg:pt-6">
      <Card className="border-destructive/50 bg-destructive/5" role="alert">
        <CardContent className="flex flex-col gap-2 text-sm">
          {problems.map((problem) => (
            <div key={problem.title + problem.detail} className="flex items-start gap-3">
              <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-destructive" />
              <p className="min-w-0 break-words">
                {problem.title && <span className="font-medium">{problem.title}. </span>}
                {problem.detail}
              </p>
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  )
}
