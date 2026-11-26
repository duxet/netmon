import type { RankedRow } from "@/components/charts/ranked-bar-chart"
import type { Group, Groups } from "@/lib/api"

/** The key of the row that folds the groups past the largest ones together. */
export const OTHER_KEY = "__other__"

/** Ranked chart rows from summary groups, with the rest folded into "Other". */
export function toRanked(
  groups: Groups | undefined,
  label: (key: string, group: Group) => string
): RankedRow[] {
  if (!groups) return []
  const rows: RankedRow[] = (groups.Items ?? []).map((group) => ({
    key: group.Key,
    label: label(group.Key, group),
    download: group.Download,
    upload: group.Upload,
    total: group.Download + group.Upload,
  }))
  const rest = groups.Rest.Download + groups.Rest.Upload
  if (rest > 0) {
    rows.push({ key: OTHER_KEY, label: "Other", download: groups.Rest.Download, upload: groups.Rest.Upload, total: rest })
  }
  return rows
}
