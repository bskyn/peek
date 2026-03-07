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
      return (
        stringValue(event.payload_json.text) ||
        stringValue(event.payload_json.subtype)
      );
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

function streamBadge(
  status: StreamStatus,
): { color: string; dotClass: string; label: string } {
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
