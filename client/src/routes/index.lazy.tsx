import {createLazyFileRoute} from '@tanstack/react-router'
import {useQuery} from "@tanstack/react-query";
import {formatBytes, formatNumber} from "../utils/formatters.ts";
import {
    ChartConfig,
    ChartContainer,
    ChartLegend,
    ChartLegendContent,
    ChartTooltip,
    ChartTooltipContent
} from '@/components/ui/chart.tsx';
import {Bar, BarChart, CartesianGrid, XAxis} from "recharts";
import {Card, CardContent, CardHeader, CardTitle} from "@/components/ui/card.tsx";

export const Route = createLazyFileRoute('/')({
    component: Index,
})

async function getStats() {
    const response = await fetch('/api/stats');
    return await response.json();
}

async function getTrafficMeasurements() {
    const response = await fetch('/api/traffic-measurements');
    return await response.json();
}

const chartConfig = {
    InBytes: {
        label: "Download",
        color: "#2563eb",
    },
    OutBytes: {
        label: "Upload",
        color: "#60a5fa",
    },
} satisfies ChartConfig

function Index() {
    const statsQuery = useQuery({queryKey: ['stats'], queryFn: getStats})
    const trafficMeasurementsQuery = useQuery({queryKey: ['traffic-measurements'], queryFn: getTrafficMeasurements})

    return (
        <>
            <div className="grid sm:grid-cols-2 lg:grid-cols-3 gap-4 sm:gap-6 mb-8">
                <Card>
                    <CardHeader>
                        <CardTitle>{statsQuery.data?.TotalClients}</CardTitle>
                    </CardHeader>
                    <CardContent>clients</CardContent>
                </Card>
                <Card>
                    <CardHeader>
                        <CardTitle>{formatBytes(statsQuery.data?.TotalBytes || 0)}</CardTitle>
                    </CardHeader>
                    <CardContent>bytes</CardContent>
                </Card>
                <Card>
                    <CardHeader>
                        <CardTitle>{formatNumber(statsQuery.data?.TotalPackets || 0)}</CardTitle>
                    </CardHeader>
                    <CardContent>packets</CardContent>
                </Card>
            </div>

            <Card>
                <CardHeader>
                    <CardTitle>Traffic</CardTitle>
                </CardHeader>
                <CardContent>
                    <ChartContainer config={chartConfig} className="min-h-[200px] w-full">
                        <BarChart accessibilityLayer data={trafficMeasurementsQuery.data}>
                            <CartesianGrid vertical={false}/>
                            <XAxis
                                dataKey="Date"
                                tickLine={false}
                                axisLine={false}
                                tickMargin={8}
                                minTickGap={32}
                                tickFormatter={(value) => {
                                    const date = new Date(value)
                                    return date.toLocaleDateString("en-US", {
                                        month: "short",
                                        day: "numeric",
                                    })
                                }}
                            />
                            <ChartTooltip content={
                                <ChartTooltipContent
                                    formatter={(value, name) => (
                                        <div className="flex min-w-[130px] items-center text-xs text-muted-foreground">
                                            {chartConfig[name as keyof typeof chartConfig]?.label || name}
                                            <div className="ml-auto flex items-baseline gap-0.5 font-mono font-medium tabular-nums text-foreground">
                                                {formatBytes(value)}
                                                { false && <span className="font-normal text-muted-foreground">
                                                  xyz
                                                </span> }
                                            </div>
                                        </div>
                                    )}
                                    labelFormatter={(value) => {
                                        return new Date(value).toLocaleDateString("en-US", {
                                            month: "short",
                                            day: "numeric",
                                            year: "numeric",
                                            hour: "numeric",
                                        })
                                    }}
                                />
                            }/>
                            <ChartLegend content={<ChartLegendContent/>}/>
                            <Bar
                                dataKey="InBytes"
                                stackId="a"
                                fill="var(--color-InBytes)"
                            />
                            <Bar
                                dataKey="OutBytes"
                                stackId="a"
                                fill="var(--color-OutBytes)"
                            />
                        </BarChart>
                    </ChartContainer>
                </CardContent>
            </Card>
        </>
    )
}
