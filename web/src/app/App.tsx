import { useEffect } from "react";

import { EmptyPanel } from "../components/EmptyPanel";
import { SessionCard } from "../components/SessionCard";
import { TimelineCard } from "../components/TimelineCard";
import { useRouter } from "../hooks/useRouter";
import { useSessionDetail } from "../hooks/useSessionDetail";
import type { TimelineSort } from "../hooks/useSessionDetail";
import { useSessions } from "../hooks/useSessions";
import { buildHeaderTitle, deriveDisplayStatus, formatDateTime } from "../lib/format";
import { openStream } from "../lib/stream";
import type { SessionDetail } from "../lib/types";

export function App() {
  const { navigate, selectedSessionID } = useRouter();

  const {
    sessions,
    error: sessionsError,
    isLoading: isLoadingSessions,
    activeSessionID,
    streamStatus: listStreamStatus,
  } = useSessions();

  const {
    detail,
    setDetail,
    events,
    displayedEvents,
    error: detailError,
    isLoading: isLoadingDetail,
    streamStatus: detailStreamStatus,
    timelineSort,
    setTimelineSort,
    timelineRef,
    hasMore,
    totalCount,
  } = useSessionDetail(selectedSessionID);

  useEffect(() => {
    return openStream(
      "",
      (envelope) => {
        if (envelope.type !== "session_upsert" || envelope.session == null) return;
        const upserted = envelope.session;
        setDetail((current) => {
          if (current == null) return current;
          let next = current;
          let changed = false;
          if (current.session.id === upserted.id) {
            next = { ...next, session: upserted };
            changed = true;
          }
          if (current.root_session.id === upserted.id) {
            next = { ...next, root_session: upserted };
            changed = true;
          }
          const nextChildren = current.child_sessions.map((c) =>
            c.id === upserted.id ? upserted : c,
          );
          if (nextChildren.some((c, i) => c !== current.child_sessions[i])) {
            next = { ...next, child_sessions: nextChildren };
            changed = true;
          }
          return changed ? next : current;
        });
      },
      () => {},
    );
  }, [setDetail]);

  const selectedIsLive = selectedSessionID !== "" && selectedSessionID === activeSessionID;
  const headerTitle = buildHeaderTitle(detail, events, selectedSessionID);
  const status = deriveDisplayStatus(
    selectedSessionID,
    selectedIsLive,
    listStreamStatus,
    detailStreamStatus,
  );

  return (
    <div className="flex h-full flex-col bg-base p-3 font-sans text-[13px]">
      {/* Top bar */}
      <header className="mb-3 flex items-center justify-between gap-4 px-1">
        <h1 className="font-mono text-[14px] font-semibold text-text">{headerTitle}</h1>
        <div
          className={`flex shrink-0 items-center gap-1.5 rounded-full border border-surface-0 bg-mantle px-2.5 py-1 ${status.color}`}
        >
          <span className={`h-1.5 w-1.5 rounded-full ${status.dotClass}`} />
          <span className="text-[11px] font-medium">{status.label}</span>
        </div>
      </header>

      {/* Main workspace */}
      <main className="grid min-h-0 flex-1 grid-cols-[280px_1fr] gap-3">
        {/* Session sidebar */}
        <aside className="flex flex-col gap-2 overflow-hidden rounded-lg border border-surface-0 bg-base p-3">
          <div>
            <p className="text-[10px] font-medium uppercase tracking-[0.12em] text-overlay-0">
              Sessions
            </p>
            <h2 className="text-[13px] font-semibold text-text">Recent activity</h2>
          </div>

          {isLoadingSessions ? (
            <EmptyPanel title="Loading sessions" body="Reading stored session history." />
          ) : null}
          {!isLoadingSessions && sessionsError !== "" ? (
            <EmptyPanel title="Sessions unavailable" body={sessionsError} />
          ) : null}
          {!isLoadingSessions && sessionsError === "" && sessions.length === 0 ? (
            <EmptyPanel
              title="No sessions yet"
              body="Start `peek claude` or `peek codex` and the list will appear here."
            />
          ) : null}

          <div className="flex flex-col gap-1.5 overflow-auto pr-1">
            {sessions.map((session) => (
              <SessionCard
                key={session.id}
                session={session}
                isSelected={selectedSessionID === session.id}
                onClick={() => navigate({ kind: "session", sessionID: session.id })}
              />
            ))}
          </div>
        </aside>

        {/* Detail pane */}
        <section className="flex flex-col gap-2 overflow-hidden rounded-lg border border-surface-0 bg-base p-3">
          {selectedSessionID === "" ? (
            <EmptyPanel
              title="Choose a session"
              body="The list stays live while the timeline view focuses on one session at a time."
            />
          ) : null}

          {selectedSessionID !== "" && isLoadingDetail ? (
            <EmptyPanel title="Loading timeline" body="Fetching session metadata and events." />
          ) : null}

          {selectedSessionID !== "" && !isLoadingDetail && detailError !== "" ? (
            <EmptyPanel title="Timeline unavailable" body={detailError} />
          ) : null}

          {selectedSessionID !== "" && !isLoadingDetail && detail != null ? (
            <>
              <div className="flex items-center justify-between gap-3 border-b border-surface-0 pb-2">
                <div className="flex items-center gap-3">
                  <h2 className="text-[13px] font-semibold text-text">Timeline</h2>
                  <span className="text-[11px] tabular-nums text-overlay-0">
                    {hasMore
                      ? `${displayedEvents.length} of ${totalCount} events`
                      : `${detail.session.event_count} events`}
                  </span>
                </div>
                <div className="flex shrink-0 items-center gap-3">
                  <label className="flex items-center gap-1.5 text-[11px] text-overlay-0">
                    <span>Sort</span>
                    <select
                      value={timelineSort}
                      onChange={(e) => setTimelineSort(e.target.value as TimelineSort)}
                      className="rounded-md border border-surface-0 bg-mantle px-2 py-0.5 text-[11px] text-subtext-0"
                    >
                      <option value="asc">Asc</option>
                      <option value="desc">Desc</option>
                    </select>
                  </label>
                  <span className="text-[10px] tabular-nums text-overlay-0">
                    {formatDateTime(detail.session.updated_at)}
                  </span>
                </div>
              </div>

              <BranchStrip detail={detail} selectedSessionID={selectedSessionID} navigate={navigate} />

              <div
                ref={timelineRef}
                className="flex min-h-0 flex-1 flex-col gap-1.5 overflow-auto pr-1"
              >
                {displayedEvents.length === 0 ? (
                  <EmptyPanel
                    title="No events yet"
                    body="Live events will appear here as the session advances."
                  />
                ) : (
                  displayedEvents.map((event) => <TimelineCard key={event.id} event={event} />)
                )}
              </div>
            </>
          ) : null}
        </section>
      </main>
    </div>
  );
}

function BranchStrip({
  detail,
  selectedSessionID,
  navigate,
}: {
  detail: SessionDetail;
  selectedSessionID: string;
  navigate: (route: { kind: "session"; sessionID: string }) => void;
}) {
  const branchSessions = [detail.root_session, ...detail.child_sessions];
  if (branchSessions.length <= 1) return null;

  return (
    <div className="flex flex-wrap gap-1">
      {branchSessions.map((session) => (
        <button
          key={session.id}
          type="button"
          onClick={() => navigate({ kind: "session", sessionID: session.id })}
          className={`cursor-pointer rounded-md border px-2 py-0.5 font-mono text-[11px] transition-colors ${
            selectedSessionID === session.id
              ? "border-lavender/30 bg-lavender/8 text-lavender"
              : "border-surface-0 bg-mantle text-overlay-0 hover:text-overlay-1"
          }`}
        >
          {session.id === detail.root_session.id ? "Root" : session.source_session_id}
        </button>
      ))}
    </div>
  );
}
