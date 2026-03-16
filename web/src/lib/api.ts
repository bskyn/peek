import { normalizeSessionDetail, normalizeSessionSummary, normalizeViewerEvent, normalizeViewerStatus } from './normalize';
import type { EventPage, SessionDetail, SessionSummary, ViewerStatus } from './types';

async function requestJSON(input: string, init?: RequestInit): Promise<unknown> {
  const response = await fetch(input, init);
  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || `request failed: ${response.status}`);
  }
  return response.json();
}

export async function fetchSessions(runtimeID?: string): Promise<SessionSummary[]> {
  const suffix = runtimeID ? `?runtime_id=${encodeURIComponent(runtimeID)}` : '';
  const payload = await requestJSON(`/api/sessions${suffix}`);
  const sessions = Array.isArray((payload as { sessions?: unknown }).sessions)
    ? (payload as { sessions: unknown[] }).sessions
    : [];
  return sessions.map(normalizeSessionSummary).filter((entry): entry is SessionSummary => entry != null);
}

export async function fetchSessionDetail(sessionID: string): Promise<SessionDetail> {
  const payload = await requestJSON(`/api/sessions/${encodeURIComponent(sessionID)}`);
  return normalizeSessionDetail(payload);
}

export async function fetchSessionEvents(sessionID: string): Promise<EventPage> {
  const encoded = encodeURIComponent(sessionID);
  const allEvents: EventPage['events'] = [];
  let afterSeq = -1;

  while (true) {
    const payload = (await requestJSON(
      `/api/sessions/${encoded}/events?limit=500&after_seq=${afterSeq}`,
    )) as {
      events?: unknown;
      has_more?: unknown;
      next_after_seq?: unknown;
    };
    const pageEvents = Array.isArray(payload.events)
      ? payload.events.map(normalizeViewerEvent).filter((entry): entry is EventPage['events'][number] => entry != null)
      : [];
    const hasMore = payload.has_more === true;
    const nextAfterSeq =
      typeof payload.next_after_seq === 'number' ? payload.next_after_seq : undefined;
    allEvents.push(...pageEvents);
    if (!hasMore) {
      return { events: allEvents, has_more: false, next_after_seq: nextAfterSeq };
    }
    afterSeq = nextAfterSeq ?? afterSeq;
  }
}

export async function fetchViewerStatus(runtimeID?: string): Promise<ViewerStatus> {
  const suffix = runtimeID ? `?runtime_id=${encodeURIComponent(runtimeID)}` : '';
  const payload = await requestJSON(`/api/status${suffix}`);
  return normalizeViewerStatus(payload);
}

export async function switchRuntimeWorkspace(
  runtimeID: string,
  workspaceID: string,
): Promise<{ session_id?: string; workspace_id?: string }> {
  const payload = (await requestJSON(
    `/api/runtimes/${encodeURIComponent(runtimeID)}/workspaces/${encodeURIComponent(workspaceID)}/switch`,
    { method: 'POST' },
  )) as { session_id?: unknown; workspace_id?: unknown };
  return {
    session_id: typeof payload.session_id === 'string' ? payload.session_id : undefined,
    workspace_id: typeof payload.workspace_id === 'string' ? payload.workspace_id : undefined,
  };
}
