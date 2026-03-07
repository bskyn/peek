import type { SessionSummary } from "../lib/types";
import { formatDateTime } from "../lib/format";

export function SessionCard({
  session,
  isSelected,
  onClick,
}: {
  session: SessionSummary;
  isSelected: boolean;
  onClick: () => void;
}) {
  const sourceBadgeClass =
    session.source === "claude"
      ? "bg-peach/10 text-peach"
      : session.source === "codex"
        ? "bg-blue/10 text-blue"
        : "bg-surface-0 text-overlay-0";

  return (
    <button
      type="button"
      onClick={onClick}
      className={`group w-full cursor-pointer rounded-lg border p-2.5 text-left transition-colors ${
        isSelected
          ? "border-lavender/30 bg-lavender/8"
          : "border-border bg-mantle hover:border-surface-1 hover:bg-surface-0/40"
      }`}
    >
      <div className="flex items-center justify-between gap-2">
        <span
          className={`rounded-full px-2 py-0.5 font-mono text-[10px] font-medium uppercase tracking-wider ${sourceBadgeClass}`}
        >
          {session.source}
        </span>
        <span className="text-[10px] tabular-nums text-overlay-0">
          {formatDateTime(session.updated_at)}
        </span>
      </div>

      <p className="mt-1.5 truncate font-mono text-[11px] font-medium text-text">
        {session.source_session_id || session.id}
      </p>
      <p className="mt-0.5 truncate text-[11px] text-overlay-1">
        {session.project_path || "Unknown project"}
      </p>
      <p className="mt-0.5 text-[10px] tabular-nums text-overlay-0">
        {session.event_count} events
      </p>
    </button>
  );
}
