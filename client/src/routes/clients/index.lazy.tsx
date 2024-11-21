import {createLazyFileRoute, Link} from '@tanstack/react-router'
import {useQuery} from "@tanstack/react-query";
import {
    Table,
    TableBody,
    TableCaption,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import {formatBytes, formatNumber} from "../../utils/formatters.ts";

export const Route = createLazyFileRoute('/clients/')({
    component: Clients,
})

async function getClients() {
    const response = await fetch('/api/clients');
    const json = await response.json();

    console.log(json);

    return json;
}

function Clients() {
    const query = useQuery({queryKey: ['clients'], queryFn: getClients})

    return (
        <Table>
            <TableCaption>Clients</TableCaption>
            <TableHeader>
                <TableRow>
                    <TableHead className="w-[180px]">MAC address</TableHead>
                    <TableHead className="w-[120px]">IP address</TableHead>
                    <TableHead>Hostname</TableHead>
                    <TableHead className="text-right">In bytes</TableHead>
                    <TableHead className="text-right">Out bytes</TableHead>
                    <TableHead className="text-right">Total bytes</TableHead>
                </TableRow>
            </TableHeader>
            <TableBody>
                {query.data && query.data.map((client: any) =>
                    <TableRow>
                        <TableCell>
                            <Link to={`/clients/${client.ID}`}>
                                {client.MACAddress}
                            </Link>
                        </TableCell>
                        <TableCell>
                            <Link to={`/clients/${client.MACAddress}`}>
                                {client.IPAddresses[0]}
                            </Link>
                        </TableCell>
                        <TableCell>
                            <Link to={`/clients/${client.MACAddress}`}>
                            {client.Hostname}
                            </Link>
                        </TableCell>
                        <TableCell className="text-right">{formatBytes(client.Traffic.InBytes)}</TableCell>
                        <TableCell className="text-right">{formatBytes(client.Traffic.OutBytes)}</TableCell>
                        <TableCell className="text-right">{formatBytes(client.Traffic.InBytes + client.Traffic.OutBytes)}</TableCell>
                    </TableRow>
                )}
            </TableBody>
        </Table>
    )
}
