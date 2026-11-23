import {useQuery} from "@tanstack/react-query";
import {Card, CardContent, CardHeader, CardTitle} from "@/components/ui/card.tsx";
import {formatBytes} from "@/utils/formatters.ts";

type AppTraffic = {
    Name: string
    Traffic: {
        InBytes: number
        InPackets: number
        OutBytes: number
        OutPackets: number
    }
}

const OTHER = "Other"

async function getAppTraffic(ip?: string): Promise<AppTraffic[]> {
    const params = ip ? '?' + new URLSearchParams({ip}) : ''
    const response = await fetch(`/api/apps${params}`);
    return await response.json() ?? [];
}

export function AppTrafficCard({ip}: { ip?: string }) {
    const query = useQuery({queryKey: ['apps', ip ?? null], queryFn: () => getAppTraffic(ip)})
    const apps = query.data ?? []

    const total = apps.reduce((sum, app) => sum + app.Traffic.InBytes + app.Traffic.OutBytes, 0)
    const max = apps
        .filter(app => app.Name !== OTHER)
        .reduce((m, app) => Math.max(m, app.Traffic.InBytes + app.Traffic.OutBytes), 0)

    return (
        <Card>
            <CardHeader>
                <CardTitle>Traffic by app</CardTitle>
            </CardHeader>
            <CardContent>
                {query.isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
                {!query.isLoading && apps.length === 0 &&
                    <p className="text-sm text-muted-foreground">No internet traffic recorded yet.</p>}

                <ul className="space-y-3">
                    {apps.map(app => {
                        const appTotal = app.Traffic.InBytes + app.Traffic.OutBytes
                        const share = total > 0 ? appTotal / total * 100 : 0
                        const isOther = app.Name === OTHER
                        // "Other" usually dwarfs everything else, so it's not used to scale the bars.
                        const scale = isOther ? total : max
                        const inWidth = scale > 0 ? app.Traffic.InBytes / scale * 100 : 0
                        const outWidth = scale > 0 ? app.Traffic.OutBytes / scale * 100 : 0

                        return (
                            <li key={app.Name}>
                                <div className="flex items-baseline justify-between gap-4 text-sm">
                                    <span className={isOther ? "text-muted-foreground" : "font-medium"}>
                                        {isOther ? "Other / unrecognized" : app.Name}
                                    </span>
                                    <span className="font-mono tabular-nums whitespace-nowrap">
                                        {formatBytes(appTotal)}
                                        <span className="ml-2 text-muted-foreground">{share.toFixed(1)}%</span>
                                    </span>
                                </div>
                                <div className="mt-1 flex h-2 w-full overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800"
                                     title={`Download ${formatBytes(app.Traffic.InBytes)} · Upload ${formatBytes(app.Traffic.OutBytes)}`}>
                                    <div className={isOther ? "bg-gray-400" : "bg-[#2563eb]"} style={{width: `${inWidth}%`}}/>
                                    <div className={isOther ? "bg-gray-300" : "bg-[#60a5fa]"} style={{width: `${outWidth}%`}}/>
                                </div>
                                <div className="mt-1 text-xs text-muted-foreground">
                                    ↓ {formatBytes(app.Traffic.InBytes)} · ↑ {formatBytes(app.Traffic.OutBytes)}
                                </div>
                            </li>
                        )
                    })}
                </ul>
            </CardContent>
        </Card>
    )
}
