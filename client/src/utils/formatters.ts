import humanFormat from "human-format";
import prettyBytes from "pretty-bytes";
import {findByNumber} from "ip-protocols";

export function formatBytes(bytes: number): string {
    return prettyBytes(bytes);
}

export function formatNumber(number: number): string {
    return humanFormat(number, { separator: "" });
}

export function formatIPProtocol(ipProtocol: number): string {
    return findByNumber(ipProtocol).Name;
}
