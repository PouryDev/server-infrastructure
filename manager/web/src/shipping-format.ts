import { bytes, number } from "./format";
import type { PreservedLimit } from "./env-types";

export function describeLimit(l: PreservedLimit): string {
  const parts: string[] = [];
  if (l.memoryBytes > 0) parts.push(`حافظه ${bytes(l.memoryBytes)}`);
  if (l.nanoCpus > 0) parts.push(`CPU ${number(l.nanoCpus / 1e9, 2)}`);
  if (l.memSwapBytes > 0) parts.push(`حافظه + swap ${bytes(l.memSwapBytes)}`);
  return parts.join(" · ");
}
