import type { CollectorStatus } from "@/lib/api"
import { formatDayTime } from "@/lib/format"

export interface CollectorProblem {
  /** What is wrong, in a few words; empty when detail says it all. */
  title: string
  detail: string
}

/** What keeps netmon from recording traffic, or all of it, worst first. */
export function collectorProblems(collector: CollectorStatus | null): CollectorProblem[] {
  if (!collector) return []

  const problems: CollectorProblem[] = []
  if (!collector.Running) {
    problems.push({
      title: "netmon isn't recording traffic",
      detail: collector.Error ?? "The collector didn't start; netmon's log says why.",
    })
  } else if (collector.Error) {
    problems.push({ title: "Recording traffic failed", detail: `${collector.Error}. netmon tries again every minute.` })
  } else if (collector.Stale && collector.LastRecorded) {
    problems.push({
      title: "Recording seems stuck",
      detail: `Traffic was last recorded at ${formatDayTime(new Date(collector.LastRecorded))}.`,
    })
  }
  for (const warning of collector.Warnings) {
    problems.push({ title: "", detail: warning })
  }
  return problems
}
