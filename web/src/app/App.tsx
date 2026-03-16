import { startTransition, useCallback, useEffect, useRef, useState } from 'react';
import { Outlet, useNavigate, useRouterState } from '@tanstack/react-router';

import { CostSidebar } from '../components/CostSidebar';
import { EmptyPanel } from '../components/EmptyPanel';
import { SessionCard } from '../components/SessionCard';
import { TimelineCard } from '../components/TimelineCard';
import { useSessionDetail } from '../hooks/useSessionDetail';
import type { TimelineSort } from '../hooks/useSessionDetail';
import { useSessions } from '../hooks/useSessions';
import { switchRuntimeWorkspace } from '../lib/api';
import { buildHeaderTitle, deriveDisplayStatus, formatDateTime } from '../lib/format';
import { openStream } from '../lib/stream';

export function App() {
  const routerState = useRouterState();
  const tanstackNavigate = useNavigate();

  const sessionMatch = routerState.matches.find((m) => m.routeId === '/sessions/$sessionId');
  const runtimeMatch = routerState.matches.find((m) => m.routeId === '/r/$runtimeId');
  const runtimeSessionMatch = routerState.matches.find(
    (m) => m.routeId === '/r/$runtimeId/sessions/$sessionId',
  );
  const selectedRuntimeID =
    (runtimeSessionMatch?.params as Record<string, string> | undefined)?.runtimeId ??
    (runtimeMatch?.params as Record<string, string> | undefined)?.runtimeId ??
    '';
  const selectedSessionID =
    (runtimeSessionMatch?.params as Record<string, string> | undefined)?.sessionId ??
    (sessionMatch?.params as Record<string, string> | undefined)?.sessionId ??
    '';
  const [switchError, setSwitchError] = useState('');
  const [switchingWorkspaceID, setSwitchingWorkspaceID] = useState('');

  const navigateToSession = useCallback(
    (sessionId: string) => {
      if (selectedRuntimeID !== '') {
        tanstackNavigate({
          to: '/r/$runtimeId/sessions/$sessionId',
          params: { runtimeId: selectedRuntimeID, sessionId },
        });
        return;
      }
      tanstackNavigate({ to: '/sessions/$sessionId', params: { sessionId } });
    },
    [selectedRuntimeID, tanstackNavigate],
  );

  const navigateToRuntime = useCallback(
    (runtimeId: string, sessionId?: string) => {
      if (sessionId != null && sessionId !== '') {
        tanstackNavigate({
          to: '/r/$runtimeId/sessions/$sessionId',
          params: { runtimeId, sessionId },
        });
        return;
      }
      tanstackNavigate({ to: '/r/$runtimeId', params: { runtimeId } });
    },
    [tanstackNavigate],
  );

  const {
    sessions,
    error: sessionsError,
    isLoading: isLoadingSessions,
    activeSessionID,
    currentRuntimeID,
    runtime,
    runtimes,
    workspaces,
    streamStatus: listStreamStatus,
  } = useSessions(selectedRuntimeID);

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
      '',
      (envelope) => {
        if (envelope.type !== 'session_upsert' || envelope.session == null) return;
        const upserted = envelope.session;
        setDetail((current) => {
          if (current == null || current.session.id !== upserted.id) return current;
          return { ...current, session: upserted };
        });
      },
      () => {},
    );
  }, [setDetail]);

  const selectedIsLive = selectedSessionID !== '' && selectedSessionID === activeSessionID;
  const headerTitle = buildHeaderTitle(detail, events, selectedSessionID);
  const status = deriveDisplayStatus(
    selectedSessionID,
    selectedIsLive,
    listStreamStatus,
    detailStreamStatus,
  );
  const previousActiveSessionIDRef = useRef(activeSessionID);

  const hasSession = selectedSessionID !== '';
  const showDetail = hasSession && !isLoadingDetail && detail != null;

  useEffect(() => {
    if (selectedRuntimeID === '' || selectedSessionID !== '' || activeSessionID === '') {
      return;
    }
    startTransition(() => {
      navigateToRuntime(selectedRuntimeID, activeSessionID);
    });
  }, [activeSessionID, navigateToRuntime, selectedRuntimeID, selectedSessionID]);

  useEffect(() => {
    const previousActiveSessionID = previousActiveSessionIDRef.current;
    previousActiveSessionIDRef.current = activeSessionID;

    if (selectedRuntimeID === '' || selectedSessionID === '' || activeSessionID === '') {
      return;
    }
    if (selectedSessionID === activeSessionID) {
      return;
    }
    if (selectedSessionID !== previousActiveSessionID) {
      return;
    }

    startTransition(() => {
      navigateToRuntime(selectedRuntimeID, activeSessionID);
    });
  }, [activeSessionID, navigateToRuntime, selectedRuntimeID, selectedSessionID]);

  const handleWorkspaceSwitch = useCallback(
    async (workspaceId: string) => {
      if (selectedRuntimeID === '') return;
      setSwitchError('');
      setSwitchingWorkspaceID(workspaceId);
      try {
        const result = await switchRuntimeWorkspace(selectedRuntimeID, workspaceId);
        startTransition(() => {
          if (result.session_id != null && result.session_id !== '') {
            navigateToRuntime(selectedRuntimeID, result.session_id);
            return;
          }
          navigateToRuntime(selectedRuntimeID);
        });
      } catch (error) {
        setSwitchError(error instanceof Error ? error.message : 'Workspace switch failed');
      } finally {
        setSwitchingWorkspaceID('');
      }
    },
    [navigateToRuntime, selectedRuntimeID],
  );

  const managedSidebarSessions = workspaces.flatMap((entry) =>
    entry.latest_session != null ? [entry.latest_session] : [],
  );
  const sidebarSessions = selectedRuntimeID !== '' ? managedSidebarSessions : sessions;
  const sidebarLabel = selectedRuntimeID !== '' ? 'Managed Runtime' : 'Sessions';
  const sidebarTitle = selectedRuntimeID !== '' ? 'Runtime sessions' : 'Recent activity';
  const emptySidebarBody =
    selectedRuntimeID !== ''
      ? 'Branches and workspace resumes for this runtime will appear here.'
      : 'Start `peek claude` or `peek codex` and the list will appear here.';

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

      {runtime?.enabled ? (
        <section className="mb-3 flex items-center justify-between gap-3 rounded-lg border border-surface-0 bg-base px-4 py-3">
          <div className="min-w-0">
            <p className="text-[10px] font-medium uppercase tracking-[0.12em] text-overlay-0">
              Workspace Runtime
            </p>
            <div className="flex flex-wrap items-center gap-2 text-[12px] text-subtext-0">
              <span className="font-medium text-text">{runtime.phase}</span>
              {runtime.active_workspace_id ? <span>{runtime.active_workspace_id}</span> : null}
              {runtime.message ? <span>{runtime.message}</span> : null}
              {runtime.bootstrap.reused ? <span>bootstrap reused</span> : null}
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {runtime.services.map((service) => (
              <span
                key={service.name}
                className="rounded-full border border-surface-0 bg-mantle px-2 py-1 text-[11px] text-subtext-0"
              >
                {service.name}: {service.status}
              </span>
            ))}
            {selectedRuntimeID !== '' ? (
              <a
                href={`/r/${selectedRuntimeID}/app/`}
                className="rounded-full border border-lavender/30 bg-lavender/10 px-3 py-1 text-[11px] font-medium text-lavender"
              >
                Open app
              </a>
            ) : null}
          </div>
        </section>
      ) : null}

      {runtimes.length > 0 ? (
        <section className="mb-3 flex flex-wrap gap-2">
          {runtimes.map((entry) => {
            const isCurrent = entry.runtime.id === currentRuntimeID;
            const appHref =
              entry.companion?.browser_path_prefix != null &&
              entry.companion.browser_path_prefix !== ''
                ? `/r/${entry.runtime.id}/app/`
                : '';
            return (
              <div
                key={entry.runtime.id}
                className={`min-w-[220px] rounded-lg border px-3 py-2 ${
                  isCurrent ? 'border-lavender/40 bg-lavender/10' : 'border-surface-0 bg-base'
                }`}
              >
                <div className="flex items-center justify-between gap-3">
                  <div>
                    <p className="font-mono text-[11px] text-text">{entry.runtime.id}</p>
                    <p className="text-[10px] uppercase tracking-[0.12em] text-overlay-0">
                      {entry.runtime.source} · {entry.runtime.status}
                    </p>
                  </div>
                  <button
                    type="button"
                    onClick={() =>
                      navigateToRuntime(entry.runtime.id, entry.runtime.active_session_id)
                    }
                    className="rounded-full border border-surface-0 bg-mantle px-2.5 py-1 text-[10px] font-medium text-subtext-0"
                  >
                    View
                  </button>
                  {appHref !== '' ? (
                    <a
                      href={appHref}
                      className="rounded-full border border-lavender/30 bg-lavender/10 px-2.5 py-1 text-[10px] font-medium text-lavender"
                    >
                      Open app
                    </a>
                  ) : null}
                </div>
                <p className="mt-2 text-[11px] text-subtext-0">
                  workspace {entry.runtime.active_workspace_id}
                </p>
                {entry.checkout?.workspace_id === entry.runtime.root_workspace_id ? (
                  <p className="text-[10px] text-overlay-0">reusing primary checkout</p>
                ) : (
                  <p className="text-[10px] text-overlay-0">isolated root worktree</p>
                )}
              </div>
            );
          })}
        </section>
      ) : null}

      {selectedRuntimeID !== '' && workspaces.length > 0 ? (
        <section className="mb-3 rounded-lg border border-surface-0 bg-base px-4 py-3">
          <div className="flex items-center justify-between gap-3">
            <div>
              <p className="text-[10px] font-medium uppercase tracking-[0.12em] text-overlay-0">
                Runtime Workspaces
              </p>
              <h2 className="text-[13px] font-semibold text-text">
                Switch the active worktree for this runtime
              </h2>
            </div>
            {switchError !== '' ? <p className="text-[11px] text-red">{switchError}</p> : null}
          </div>
          <div className="mt-3 flex flex-wrap gap-2">
            {workspaces.map((entry) => {
              return (
                <button
                  key={entry.workspace.id}
                  type="button"
                  disabled={entry.is_active || switchingWorkspaceID === entry.workspace.id}
                  onClick={() => void handleWorkspaceSwitch(entry.workspace.id)}
                  className={`rounded-lg border px-3 py-2 text-left ${
                    entry.is_active
                      ? 'border-lavender/40 bg-lavender/10'
                      : 'border-surface-0 bg-mantle disabled:opacity-60'
                  }`}
                >
                  <p className="font-mono text-[11px] text-text">{entry.workspace.id}</p>
                  <p className="text-[10px] uppercase tracking-[0.12em] text-overlay-0">
                    {entry.workspace.status}
                  </p>
                  {entry.latest_session != null ? (
                    <p className="mt-1 text-[10px] text-subtext-0">{entry.latest_session.id}</p>
                  ) : null}
                </button>
              );
            })}
          </div>
        </section>
      ) : null}

      {/* Main workspace */}
      <main
        className={`grid min-h-0 flex-1 gap-3 ${
          showDetail ? 'grid-cols-[280px_1fr_260px]' : 'grid-cols-[280px_1fr]'
        }`}
      >
        {/* Session sidebar */}
        <aside className="flex flex-col gap-2 overflow-hidden rounded-lg border border-surface-0 bg-base p-3">
          <div>
            <p className="text-[10px] font-medium uppercase tracking-[0.12em] text-overlay-0">
              {sidebarLabel}
            </p>
            <h2 className="text-[13px] font-semibold text-text">{sidebarTitle}</h2>
          </div>

          {isLoadingSessions ? (
            <EmptyPanel title="Loading sessions" body="Reading stored session history." />
          ) : null}
          {!isLoadingSessions && sessionsError !== '' ? (
            <EmptyPanel title="Sessions unavailable" body={sessionsError} />
          ) : null}
          {!isLoadingSessions && sessionsError === '' && sidebarSessions.length === 0 ? (
            <EmptyPanel
              title="No sessions yet"
              body={emptySidebarBody}
            />
          ) : null}

          <div className="flex flex-col gap-1.5 overflow-auto pr-1">
            {sidebarSessions.map((session) => (
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

          {hasSession && !isLoadingDetail && detailError !== '' ? (
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
