import { useEffect, useState } from 'react';

import { fetchSessions, fetchViewerStatus } from '../lib/api';
import { openStream } from '../lib/stream';
import type { LiveEnvelope, RuntimeStatus, SessionSummary, StreamStatus } from '../lib/types';

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

export function useSessions() {
  const [sessions, setSessions] = useState<SessionSummary[]>([]);
  const [error, setError] = useState('');
  const [isLoading, setIsLoading] = useState(true);
  const [activeSessionID, setActiveSessionID] = useState('');
  const [runtime, setRuntime] = useState<RuntimeStatus | undefined>(undefined);
  const [streamStatus, setStreamStatus] = useState<StreamStatus>('connecting');

  // Initial fetch — merges with any SSE data that arrived first
  useEffect(() => {
    let cancelled = false;
    setIsLoading(true);

    Promise.all([fetchSessions(), fetchViewerStatus()])
      .then(([nextSessions, status]) => {
        if (cancelled) return;
        // Merge fetched data with any SSE updates that arrived during the fetch
        setSessions((current) => mergeSessions(nextSessions, current));
        setActiveSessionID((current) => current || (status.active_session_id ?? ''));
        setRuntime(status.runtime);
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
  }, []);

  // Live stream for session list
  useEffect(() => {
    return openStream(
      '',
      (envelope: LiveEnvelope) => {
        if (envelope.type === 'active_session') {
          setActiveSessionID(envelope.active_session_id ?? '');
          return;
        }
        if (envelope.type === 'runtime_status') {
          setRuntime(envelope.runtime);
          return;
        }
        if (envelope.type !== 'session_upsert' || envelope.session == null) return;
        setSessions((cur) => sortSessions(upsertSession(cur, envelope.session!)));
      },
      setStreamStatus,
    );
  }, []);

  return { sessions, error, isLoading, activeSessionID, runtime, streamStatus };
}
