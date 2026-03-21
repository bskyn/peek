import { normalizeSessionDetail, normalizeSessionSummary, normalizeViewerEvent, normalizeViewerStatus } from './normalize';
import type {
  EventPage,
  SessionDetail,
  SessionSummary,
  ViewerStatus,
  WorkspaceSwitchResponse,
} from './types';

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

export async function fetchSessionEvents(
  sessionID: string,
  options?: { beforeSeq?: number; limit?: number; tail?: boolean },
): Promise<EventPage> {
  const encoded = encodeURIComponent(sessionID);
  const params = new URLSearchParams();
  params.set('limit', String(options?.limit ?? 200));
  if (options?.beforeSeq != null) {
    params.set('before_seq', String(options.beforeSeq));
  }
  if (options?.tail === true) {
    params.set('tail', 'true');
  }

  const payload = (await requestJSON(`/api/sessions/${encoded}/events?${params.toString()}`)) as {
    events?: unknown;
    has_more?: unknown;
    next_after_seq?: unknown;
    next_before_seq?: unknown;
  };
  const pageEvents = Array.isArray(payload.events)
    ? payload.events
        .map(normalizeViewerEvent)
        .filter((entry): entry is EventPage['events'][number] => entry != null)
    : [];
  return {
    events: pageEvents,
    has_more: payload.has_more === true,
    next_after_seq: typeof payload.next_after_seq === 'number' ? payload.next_after_seq : undefined,
    next_before_seq:
      typeof payload.next_before_seq === 'number' ? payload.next_before_seq : undefined,
  };
}

export async function fetchViewerStatus(runtimeID?: string): Promise<ViewerStatus> {
  const suffix = runtimeID ? `?runtime_id=${encodeURIComponent(runtimeID)}` : '';
  const payload = await requestJSON(`/api/status${suffix}`);
  return normalizeViewerStatus(payload);
}

export async function switchRuntimeWorkspace(
  runtimeID: string,
  workspaceID: string,
): Promise<WorkspaceSwitchResponse> {
  const payload = (await requestJSON(
    `/api/runtimes/${encodeURIComponent(runtimeID)}/workspaces/${encodeURIComponent(workspaceID)}/switch`,
    { method: 'POST' },
  )) as { session_id?: unknown; workspace_id?: unknown };
  return {
    session_id: typeof payload.session_id === 'string' ? payload.session_id : undefined,
    workspace_id: typeof payload.workspace_id === 'string' ? payload.workspace_id : undefined,
  };
}
