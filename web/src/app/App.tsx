import { useCallback, useEffect } from "react";
import { Outlet, useNavigate, useRouterState } from "@tanstack/react-router";

import { CostSidebar } from "../components/CostSidebar";
import { EmptyPanel } from "../components/EmptyPanel";
import { SessionCard } from "../components/SessionCard";
import { TimelineCard } from "../components/TimelineCard";
import { useSessionDetail } from "../hooks/useSessionDetail";
import type { TimelineSort } from "../hooks/useSessionDetail";
import { useSessions } from "../hooks/useSessions";
import { buildHeaderTitle, deriveDisplayStatus, formatDateTime } from "../lib/format";
import { openStream } from "../lib/stream";

export function App() {
  const routerState = useRouterState();
  const tanstackNavigate = useNavigate();

  const sessionMatch = routerState.matches.find((m) => m.routeId === "/sessions/$sessionId");
  const selectedSessionID =
    (sessionMatch?.params as Record<string, string> | undefined)?.sessionId ?? "";

  const navigateToSession = useCallback(
    (sessionId: string) => {
      tanstackNavigate({ to: "/sessions/$sessionId", params: { sessionId } });
    },
    [tanstackNavigate],
  );

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

  // Keep session metadata in sync via SSE
  useEffect(() => {
    return openStream(
      "",
      (envelope) => {
        if (envelope.type !== "session_upsert" || envelope.session == null) return;
        const upserted = envelope.session;
        setDetail((current) => {
          if (current == null || current.session.id !== upserted.id) return current;
          return { ...current, session: upserted };
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

  const hasSession = selectedSessionID !== "";
  const showDetail = hasSession && !isLoadingDetail && detail != null;

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
      <main
        className={`grid min-h-0 flex-1 gap-3 ${
          showDetail ? "grid-cols-[280px_1fr_260px]" : "grid-cols-[280px_1fr]"
        }`}
      >
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
                onClick={() => navigateToSession(session.id)}
              />
            ))}
          </div>
        </aside>

        {/* Detail pane */}
        <section className="flex flex-col gap-2 overflow-hidden rounded-lg border border-surface-0 bg-base p-3">
          {!hasSession ? (
            <EmptyPanel
              title="Choose a session"
              body="The list stays live while the timeline view focuses on one session at a time."
            />
          ) : null}

          {hasSession && isLoadingDetail ? (
            <EmptyPanel title="Loading timeline" body="Fetching session metadata and events." />
          ) : null}

          {hasSession && !isLoadingDetail && detailError !== "" ? (
            <EmptyPanel title="Timeline unavailable" body={detailError} />
          ) : null}

          {showDetail ? (
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

        {/* Cost sidebar */}
        {showDetail ? <CostSidebar events={events} /> : null}
      </main>

      {/* TanStack Router outlet (child routes have no visible output) */}
      <Outlet />
    </div>
  );
}
