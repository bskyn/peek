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
import type { ViewerShellFollowHint, ViewerStatus, ViewerWorkspaceTransition } from '../lib/types';

const runtimeStatusPollAttempts = 6;
const runtimeStatusPollDelayMS = 250;

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

async function waitForRuntimeSwitchConvergence(
  refreshStatus: () => Promise<ViewerStatus>,
  targetWorkspaceID: string,
): Promise<ViewerStatus | null> {
  let lastStatus: ViewerStatus | null = null;

  for (let attempt = 0; attempt < runtimeStatusPollAttempts; attempt += 1) {
    try {
      const status = await refreshStatus();
      lastStatus = status;
      if (status.runtime?.active_workspace_id === targetWorkspaceID) {
        return status;
      }
    } catch {
      // Fall back to the switch response if the status refresh is briefly unavailable.
    }

    if (attempt < runtimeStatusPollAttempts - 1) {
      await sleep(runtimeStatusPollDelayMS);
    }
  }

  return lastStatus;
}

function buildShellFollowHint(runtimeID: string): ViewerShellFollowHint {
  return {
    runtime_id: runtimeID,
    init_command: 'eval "$(peek shell init zsh)"',
    attach_command: `eval "$(peek shell attach ${runtimeID})"`,
    status_command: 'peek shell status',
  };
}

function workspaceTransitionSummary(
  transition: ViewerWorkspaceTransition,
  activeWorkspaceID: string,
): { toneClass: string; title: string; body: string } {
  switch (transition.status) {
    case 'idle':
      return {
        toneClass: 'border-surface-0 bg-mantle',
        title: activeWorkspaceID === '' ? 'Runtime workspace is ready' : `Active workspace: ${activeWorkspaceID}`,
        body:
          activeWorkspaceID === ''
            ? 'Pick a workspace to move this runtime and reroute the timeline to the resumed session.'
            : 'Viewer switches stay scoped to this runtime lineage and reroute the timeline after the runtime settles.',
      };
    case 'switching':
      return {
        toneClass: 'border-yellow/30 bg-yellow/10',
        title: `Switching runtime to ${transition.requested_workspace_id}`,
        body:
          'Waiting for the managed runtime to settle before the viewer confirms the active workspace and timeline session.',
      };
    case 'converged': {
      const finalWorkspaceID =
        transition.active_workspace_id ||
        transition.response_workspace_id ||
        transition.requested_workspace_id;
      const finalSessionID = transition.active_session_id || transition.response_session_id;
      return {
        toneClass: 'border-green/30 bg-green/10',
        title: `Runtime now active on ${finalWorkspaceID}`,
        body:
          finalSessionID != null && finalSessionID !== ''
            ? `Timeline moved to resumed session ${finalSessionID}.`
            : 'Runtime status refreshed and the viewer stayed on the selected runtime.',
      };
    }
    case 'failed':
      return {
        toneClass: 'border-red/30 bg-red/10',
        title: `Switch to ${transition.requested_workspace_id} failed`,
        body: transition.error,
      };
  }
}

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
  const [switchingWorkspaceID, setSwitchingWorkspaceID] = useState('');
  const [switchTransition, setSwitchTransition] = useState<ViewerWorkspaceTransition>({
    status: 'idle',
  });
  const [shellCommandFeedback, setShellCommandFeedback] = useState('');
  const workspaceSwitchRequestIDRef = useRef(0);

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
    refreshStatus,
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
    isLoadingOlder,
    loadOlder,
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
  const activeWorkspaceID =
    runtime?.active_workspace_id || workspaces.find((entry) => entry.is_active)?.workspace.id || '';
  const visibleTransition =
    switchTransition.status === 'idle' || switchTransition.runtime_id === selectedRuntimeID
      ? switchTransition
      : ({ status: 'idle' } as const);
  const shellFollowHint =
    selectedRuntimeID === '' ? undefined : buildShellFollowHint(selectedRuntimeID);
  const transitionSummary = workspaceTransitionSummary(visibleTransition, activeWorkspaceID);

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

  useEffect(() => {
    setShellCommandFeedback('');
    setSwitchTransition((current) => {
      if (current.status === 'idle') {
        return current;
      }
      if (current.runtime_id === selectedRuntimeID) {
        return current;
      }
      return { status: 'idle' };
    });
  }, [selectedRuntimeID]);

  const handleCopyShellCommand = useCallback(async (label: string, command: string) => {
    if (typeof navigator === 'undefined' || navigator.clipboard == null) {
      setShellCommandFeedback(`Clipboard unavailable. Run: ${command}`);
      return;
    }
    try {
      await navigator.clipboard.writeText(command);
      setShellCommandFeedback(`${label} copied to the clipboard.`);
    } catch {
      setShellCommandFeedback(`Copy failed. Run: ${command}`);
    }
  }, []);

  const handleWorkspaceSwitch = useCallback(
    async (workspaceId: string) => {
      if (selectedRuntimeID === '') return;
      const requestID = workspaceSwitchRequestIDRef.current + 1;
      workspaceSwitchRequestIDRef.current = requestID;
      setShellCommandFeedback('');
      setSwitchTransition({
        status: 'switching',
        runtime_id: selectedRuntimeID,
        requested_workspace_id: workspaceId,
      });
      setSwitchingWorkspaceID(workspaceId);
      try {
        const result = await switchRuntimeWorkspace(selectedRuntimeID, workspaceId);
        const status = await waitForRuntimeSwitchConvergence(refreshStatus, workspaceId);
        if (workspaceSwitchRequestIDRef.current !== requestID) {
          return;
        }
        const finalWorkspaceID = status?.runtime?.active_workspace_id || result.workspace_id;
        const finalSessionID = status?.active_session_id || result.session_id;
        setSwitchTransition({
          status: 'converged',
          runtime_id: selectedRuntimeID,
          requested_workspace_id: workspaceId,
          response_workspace_id: result.workspace_id,
          response_session_id: result.session_id,
          active_workspace_id: finalWorkspaceID,
          active_session_id: finalSessionID,
        });
        startTransition(() => {
          if (finalSessionID != null && finalSessionID !== '') {
            navigateToRuntime(selectedRuntimeID, finalSessionID);
            return;
          }
          navigateToRuntime(selectedRuntimeID);
        });
      } catch (error) {
        if (workspaceSwitchRequestIDRef.current !== requestID) {
          return;
        }
        setSwitchTransition({
          status: 'failed',
          runtime_id: selectedRuntimeID,
          requested_workspace_id: workspaceId,
          error: error instanceof Error ? error.message : 'Workspace switch failed',
        });
      } finally {
        if (workspaceSwitchRequestIDRef.current === requestID) {
          setSwitchingWorkspaceID('');
        }
      }
    },
    [navigateToRuntime, refreshStatus, selectedRuntimeID],
  );

  const managedSidebarSessions = workspaces.flatMap((entry) =>
    entry.latest_session != null ? [entry.latest_session] : [],
  );
  const sidebarSessions = selectedRuntimeID !== '' ? managedSidebarSessions : sessions;
  const sidebarLabel = selectedRuntimeID !== '' ? 'Managed Runtime' : 'Sessions';
  const sidebarTitle = selectedRuntimeID !== '' ? 'Runtime sessions' : 'Recent activity';
  const isWorkspaceSwitchPending = switchingWorkspaceID !== '';
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
            <span className="rounded-full border border-surface-0 bg-mantle px-2.5 py-1 font-mono text-[10px] text-subtext-0">
              {selectedRuntimeID}
            </span>
          </div>
          <div className="mt-3 grid gap-3 xl:grid-cols-[minmax(0,1.15fr)_minmax(0,0.85fr)]">
            <div className={`rounded-lg border px-3 py-3 ${transitionSummary.toneClass}`}>
              <p className="text-[10px] font-medium uppercase tracking-[0.12em] text-overlay-0">
                Viewer Handoff
              </p>
              <h3 className="mt-1 text-[12px] font-semibold text-text">{transitionSummary.title}</h3>
              <p className="mt-2 text-[11px] leading-5 text-subtext-0">{transitionSummary.body}</p>
              <div className="mt-3 flex flex-wrap gap-2 text-[10px] text-overlay-0">
                {activeWorkspaceID !== '' ? (
                  <span className="rounded-full border border-surface-0 bg-base px-2 py-1">
                    active workspace {activeWorkspaceID}
                  </span>
                ) : null}
                {visibleTransition.status === 'converged' &&
                visibleTransition.active_session_id != null &&
                visibleTransition.active_session_id !== '' ? (
                  <span className="rounded-full border border-surface-0 bg-base px-2 py-1">
                    active session {visibleTransition.active_session_id}
                  </span>
                ) : null}
              </div>
            </div>
            {shellFollowHint != null ? (
              <div className="rounded-lg border border-surface-0 bg-mantle px-3 py-3">
                <p className="text-[10px] font-medium uppercase tracking-[0.12em] text-overlay-0">
                  Shell Follow
                </p>
                <h3 className="mt-1 text-[12px] font-semibold text-text">
                  Attached shells follow on the next prompt
                </h3>
                <p className="mt-2 text-[11px] leading-5 text-subtext-0">
                  Browser switches affect only shells attached to runtime{' '}
                  <span className="font-mono text-text">{shellFollowHint.runtime_id}</span>. Unhooked
                  or detached shells stay in their current cwd.
                </p>
                <div className="mt-3 flex flex-wrap gap-2">
                  <button
                    type="button"
                    onClick={() =>
                      void handleCopyShellCommand('Attach command', shellFollowHint.attach_command)
                    }
                    className="rounded-full border border-surface-0 bg-base px-2.5 py-1 text-[10px] font-medium text-subtext-0"
                  >
                    Copy attach
                  </button>
                  <button
                    type="button"
                    onClick={() =>
                      void handleCopyShellCommand('Hook setup', shellFollowHint.init_command)
                    }
                    className="rounded-full border border-surface-0 bg-base px-2.5 py-1 text-[10px] font-medium text-subtext-0"
                  >
                    Copy setup
                  </button>
                </div>
                <div className="mt-3 space-y-2">
                  <p className="rounded-md border border-surface-0 bg-base px-2.5 py-2 font-mono text-[10px] text-subtext-0">
                    {shellFollowHint.attach_command}
                  </p>
                  <p className="rounded-md border border-surface-0 bg-base px-2.5 py-2 font-mono text-[10px] text-subtext-0">
                    {shellFollowHint.init_command}
                  </p>
                  <p className="text-[10px] text-overlay-0">
                    Verify the binding with{' '}
                    <span className="font-mono text-subtext-0">{shellFollowHint.status_command}</span>.
                  </p>
                </div>
                {shellCommandFeedback !== '' ? (
                  <p className="mt-2 text-[10px] text-sky">{shellCommandFeedback}</p>
                ) : null}
              </div>
            ) : null}
          </div>
          <div className="mt-3 flex flex-wrap gap-2">
            {workspaces.map((entry) => {
              const isSwitching = switchingWorkspaceID === entry.workspace.id;
              return (
                <button
                  key={entry.workspace.id}
                  type="button"
                  disabled={entry.is_active || isWorkspaceSwitchPending}
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
                  {entry.is_active ? (
                    <p className="mt-1 text-[10px] text-lavender">active runtime workspace</p>
                  ) : null}
                  {isSwitching ? (
                    <p className="mt-1 text-[10px] text-yellow">waiting for runtime handoff...</p>
                  ) : null}
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
                    {hasMore || displayedEvents.length < totalCount
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
                {hasMore ? (
                  <div className="sticky top-0 z-10 flex justify-center pb-1 pt-0.5">
                    <button
                      type="button"
                      onClick={() => void loadOlder()}
                      disabled={isLoadingOlder}
                      className="rounded-full border border-surface-0 bg-mantle px-3 py-1 text-[10px] font-medium text-subtext-0 disabled:opacity-60"
                    >
                      {isLoadingOlder ? 'Loading earlier events...' : 'Load earlier events'}
                    </button>
                  </div>
                ) : null}
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
