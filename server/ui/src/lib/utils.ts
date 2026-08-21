import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function formatNumber(value: number) {
  return new Intl.NumberFormat("en-US", { notation: value >= 10_000 ? "compact" : "standard" }).format(value);
}

export function formatDuration(milliseconds: number) {
  if (!Number.isFinite(milliseconds) || milliseconds < 0) return "—";
  if (milliseconds < 1_000) return `${Math.round(milliseconds)} ms`;
  if (milliseconds < 60_000) return `${(milliseconds / 1_000).toFixed(milliseconds < 10_000 ? 1 : 0)} s`;
  const minutes = Math.floor(milliseconds / 60_000);
  const seconds = Math.round((milliseconds % 60_000) / 1_000);
  if (minutes >= 1_440) {
    const days = Math.floor(minutes / 1_440);
    const hours = Math.floor((minutes % 1_440) / 60);
    return `${days}d ${hours}h`;
  }
  if (minutes >= 60) {
    const hours = Math.floor(minutes / 60);
    return `${hours}h ${minutes % 60}m`;
  }
  return `${minutes}m ${seconds}s`;
}

export function formatRelativeDate(value: string) {
  const date = new Date(value);
  const delta = date.getTime() - Date.now();
  const absolute = Math.abs(delta);
  const formatter = new Intl.RelativeTimeFormat("en", { numeric: "auto" });
  if (absolute < 60_000) return formatter.format(Math.round(delta / 1_000), "second");
  if (absolute < 3_600_000) return formatter.format(Math.round(delta / 60_000), "minute");
  if (absolute < 86_400_000) return formatter.format(Math.round(delta / 3_600_000), "hour");
  return formatter.format(Math.round(delta / 86_400_000), "day");
}

export function formatTimestamp(value: string) {
  return new Intl.DateTimeFormat("en-US", {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
    second: "2-digit",
  }).format(new Date(value));
}

export function shortID(value: string, length = 9) {
  const meaningful = value.split("_").at(-1) ?? value;
  return meaningful.length > length ? meaningful.slice(0, length) : meaningful;
}

export function titleFromID(value: string) {
  return value
    .replace(/^conv_/, "")
    .replace(/_[a-f\d]{8,}$/i, "")
    .replace(/T\d{6}Z/g, "")
    .replace(/_\d{8}/g, "")
    .split(/[._/-]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ") || "Untitled session";
}
