import humanFormat from "human-format";
import prettyBytes from "pretty-bytes";

export function formatBytes(bytes: number): string {
    return prettyBytes(bytes);
}

export function formatNumber(number: number): string {
    return humanFormat(number, { separator: "" });
}
