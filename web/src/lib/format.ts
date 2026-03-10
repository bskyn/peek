import type { SessionDetail, StreamStatus, ViewerEvent } from "./types";

export function titleForEvent(event: ViewerEvent): string {
  switch (event.type) {
    case "user_message":
      return "User";
    case "assistant_thinking":
      return "Thinking";
    case "assistant_message":
      return modelLabel(event);
    case "tool_call":
      return `Tool: ${stringValue(event.payload_json.tool_name) || "call"}`;
    case "tool_result":
      return "Tool result";
    case "progress":
      if (stringValue(event.payload_json.subtype) === "token_count") return "Usage";
      return "Progress";
    case "system":
      return "System";
    case "error":
      return "Error";
  }
}

function modelLabel(event: ViewerEvent): string {
  const model = stringValue(event.payload_json.model);
  if (model !== "") return model;
  return "Assistant";
}

export function describeEvent(event: ViewerEvent): string {
  switch (event.type) {
    case "assistant_thinking":
      return stringValue(event.payload_json.thinking);
    case "tool_call":
      return prettyJSON(event.payload_json.input);
    case "progress":
      if (stringValue(event.payload_json.subtype) === "token_count") return "";
      return stringValue(event.payload_json.text) || stringValue(event.payload_json.subtype);
    case "system":
      return (
        stringValue(event.payload_json.text) ||
        stringValue(event.payload_json.subtype) ||
        prettyJSON(event.payload_json.content)
      );
    default:
      return stringValue(event.payload_json.text);
  }
}

export function extraPayload(event: ViewerEvent): string {
  if (event.type === "tool_call") return "";

  const payload = { ...event.payload_json };
  const primaryText = describeEvent(event);
  if (primaryText !== "") {
    delete payload.text;
    delete payload.thinking;
    delete payload.subtype;
    delete payload.content;
  }
  if (Object.keys(payload).length === 0) return "";
  return prettyJSON(payload);
}

export type UsageParts = {
  tokenCount: string;
  cost: string | null;
};

export function usageSummary(event: ViewerEvent): UsageParts | null {
  const usage = usageValue(event.payload_json.usage);
  if (usage == null) return null;

  return {
    tokenCount: formatTokenCount(usage.total_tokens),
    cost: usage.total_cost_usd > 0 ? formatUSD(usage.total_cost_usd) : null,
  };
}

export function formatDateTime(raw: string): string {
  const value = new Date(raw);
  if (Number.isNaN(value.getTime())) return raw;
  return value.toLocaleString([], {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}

export function buildHeaderTitle(
  detail: SessionDetail | null,
  events: ViewerEvent[],
  selectedSessionID: string,
): string {
  if (selectedSessionID === "" || detail == null) return "peek";
  const modelName = findModelName(events) || detail.session.source;
  return modelName;
}

export function deriveDisplayStatus(
  selectedSessionID: string,
  selectedIsLive: boolean,
  listStreamStatus: StreamStatus,
  detailStreamStatus: StreamStatus,
): { color: string; dotClass: string; label: string } {
  if (selectedSessionID === "") return streamBadge(listStreamStatus);
  if (!selectedIsLive)
    return {
      color: "text-overlay-1",
      dotClass: "bg-overlay-1",
      label: "History",
    };
  return streamBadge(detailStreamStatus);
}

function streamBadge(status: StreamStatus): { color: string; dotClass: string; label: string } {
  switch (status) {
    case "connecting":
      return {
        color: "text-yellow",
        dotClass: "bg-yellow",
        label: "Connecting",
      };
    case "live":
      return {
        color: "text-green",
        dotClass: "bg-green animate-pulse-live",
        label: "Live",
      };
    case "retrying":
      return {
        color: "text-yellow",
        dotClass: "bg-yellow",
        label: "Retrying",
      };
    case "disconnected":
      return { color: "text-red", dotClass: "bg-red", label: "Offline" };
  }
}

function findModelName(events: ViewerEvent[]): string {
  for (let i = events.length - 1; i >= 0; i--) {
    const model = stringValue(events[i].payload_json.model);
    if (model !== "") return model;
  }
  return "";
}

function prettyJSON(value: unknown): string {
  if (value == null || value === "") return "";
  if (typeof value === "string") return value;
  return JSON.stringify(value, null, 2);
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value : "";
}

type UsageValue = {
  total_tokens: number;
  total_cost_usd: number;
};

function usageValue(value: unknown): UsageValue | null {
  if (value == null || typeof value !== "object") return null;
  const record = value as Record<string, unknown>;
  const totalTokens = numberValue(record.total_tokens);
  const totalCost = numberValue(record.total_cost_usd);
  if (totalTokens <= 0 && totalCost <= 0) return null;
  return {
    total_tokens: totalTokens,
    total_cost_usd: totalCost,
  };
}

function numberValue(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

export function formatTokenCount(value: number): string {
  return new Intl.NumberFormat().format(value);
}

export function formatUSD(value: number): string {
  if (value <= 0) return "$0.00";

  let maximumFractionDigits = 2;
  if (value < 0.001) maximumFractionDigits = 6;
  else if (value < 0.01) maximumFractionDigits = 5;
  else if (value < 0.1) maximumFractionDigits = 4;

  return new Intl.NumberFormat(undefined, {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: 2,
    maximumFractionDigits,
  }).format(value);
}

export type AggregatedUsage = {
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
  inputCostUSD: number;
  outputCostUSD: number;
  totalCostUSD: number;
};

export function aggregateUsage(events: ViewerEvent[]): AggregatedUsage {
  let inputTokens = 0;
  let outputTokens = 0;
  let totalTokens = 0;
  let inputCostUSD = 0;
  let outputCostUSD = 0;
  let totalCostUSD = 0;

  for (const event of events) {
    const usage = event.payload_json.usage;
    if (usage == null || typeof usage !== "object") continue;
    const u = usage as Record<string, unknown>;
    inputTokens += numberValue(u.input_tokens);
    outputTokens += numberValue(u.output_tokens);
    totalTokens += numberValue(u.total_tokens);
    inputCostUSD += numberValue(u.input_cost_usd);
    outputCostUSD += numberValue(u.output_cost_usd);
    totalCostUSD += numberValue(u.total_cost_usd);
  }

  return { inputTokens, outputTokens, totalTokens, inputCostUSD, outputCostUSD, totalCostUSD };
}
