import { useCallback, useEffect, useState } from 'react';

import { fetchSessions, fetchViewerStatus } from '../lib/api';
import { openStream } from '../lib/stream';
import type {
  LiveEnvelope,
  ManagedRuntimeView,
  RuntimeWorkspaceView,
  RuntimeStatus,
  SessionSummary,
  StreamStatus,
  ViewerStatus,
} from '../lib/types';

function sortSessions(sessions: SessionSummary[]): SessionSummary[] {
  return [...sessions].sort((a, b) => {
    if (a.updated_at === b.updated_at) return a.id.localeCompare(b.id);
    return b.updated_at.localeCompare(a.updated_at);
  });
}

function mergeSessions(base: SessionSummary[], incoming: SessionSummary[]): SessionSummary[] {
  const byId = new Map<string, SessionSummary>();
  for (const s of base) byId.set(s.id, s);
  // SSE data is newer — overwrites fetched data
  for (const s of incoming) byId.set(s.id, s);
  return sortSessions(Array.from(byId.values()));
}

function upsertSession(current: SessionSummary[], next: SessionSummary): SessionSummary[] {
  return [next, ...current.filter((s) => s.id !== next.id)];
}

export function useSessions(runtimeID: string) {
  const [sessions, setSessions] = useState<SessionSummary[]>([]);
  const [error, setError] = useState('');
  const [isLoading, setIsLoading] = useState(true);
  const [activeSessionID, setActiveSessionID] = useState('');
  const [currentRuntimeID, setCurrentRuntimeID] = useState('');
  const [runtime, setRuntime] = useState<RuntimeStatus | undefined>(undefined);
  const [runtimes, setRuntimes] = useState<ManagedRuntimeView[]>([]);
  const [workspaces, setWorkspaces] = useState<RuntimeWorkspaceView[]>([]);
  const [streamStatus, setStreamStatus] = useState<StreamStatus>('connecting');

  const applyViewerStatus = useCallback((status: ViewerStatus, preserveActiveSession: boolean) => {
    setCurrentRuntimeID(status.current_runtime_id ?? '');
    if (preserveActiveSession) {
      setActiveSessionID((current) => current || (status.active_session_id ?? ''));
    } else {
      setActiveSessionID(status.active_session_id ?? '');
    }
    setRuntime(status.runtime);
    setRuntimes(status.runtimes ?? []);
    setWorkspaces(status.workspaces ?? []);
  }, []);

  const refreshStatus = useCallback(
    async (preserveActiveSession = false): Promise<ViewerStatus> => {
      const status = await fetchViewerStatus(runtimeID);
      applyViewerStatus(status, preserveActiveSession);
      return status;
    },
    [applyViewerStatus, runtimeID],
  );

  // Initial fetch — merges with any SSE data that arrived first
  useEffect(() => {
    let cancelled = false;
    setIsLoading(true);
    setSessions([]);
    setActiveSessionID('');
    setCurrentRuntimeID('');
    setRuntimes([]);
    setWorkspaces([]);
    setRuntime(undefined);

    Promise.all([fetchSessions(runtimeID), fetchViewerStatus(runtimeID)])
      .then(([nextSessions, status]) => {
        if (cancelled) return;
        // Merge fetched data with any SSE updates that arrived during the fetch
        setSessions((current) => mergeSessions(nextSessions, current));
        applyViewerStatus(status, true);
        setError('');
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setError(err instanceof Error ? err.message : 'Unknown error');
      })
      .finally(() => {
        if (!cancelled) setIsLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [applyViewerStatus, runtimeID]);

  // Live stream for session list
  useEffect(() => {
    let cancelled = false;

    const refreshViewerStatus = async () => {
      try {
        const status = await fetchViewerStatus(runtimeID);
        if (cancelled) return;
        applyViewerStatus(status, false);
      } catch {
        // Ignore transient refresh failures and let the initial fetch / next event recover.
      }
    };

    const closeStream = openStream(
      runtimeID === '' ? '' : `runtime_id=${encodeURIComponent(runtimeID)}`,
      (envelope: LiveEnvelope) => {
        if (envelope.type === 'active_session') {
          if (envelope.runtime_id != null && envelope.runtime_id !== '') {
            setCurrentRuntimeID(envelope.runtime_id);
          }
          setActiveSessionID(envelope.active_session_id ?? '');
          void refreshViewerStatus();
          return;
        }
        if (envelope.type === 'runtime_status') {
          if (envelope.runtime_id) {
            setCurrentRuntimeID(envelope.runtime_id);
          }
          setRuntime(envelope.runtime);
          return;
        }
        if (envelope.type !== 'session_upsert' || envelope.session == null) return;
        setSessions((cur) => sortSessions(upsertSession(cur, envelope.session!)));
      },
      setStreamStatus,
    );

    return () => {
      cancelled = true;
      closeStream();
    };
  }, [applyViewerStatus, runtimeID]);

  return {
    sessions,
    error,
    isLoading,
    activeSessionID,
    currentRuntimeID,
    runtime,
    runtimes,
    workspaces,
    streamStatus,
    refreshStatus,
  };
}
