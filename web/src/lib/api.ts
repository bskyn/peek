import type { EventPage, SessionDetail, SessionSummary, ViewerStatus } from './types';

async function requestJSON<T>(input: string, init?: RequestInit): Promise<T> {
  const response = await fetch(input, init);
  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || `request failed: ${response.status}`);
  }
  return (await response.json()) as T;
}

export async function fetchSessions(runtimeID?: string): Promise<SessionSummary[]> {
  const suffix = runtimeID ? `?runtime_id=${encodeURIComponent(runtimeID)}` : '';
  const payload = await requestJSON<{ sessions: SessionSummary[] }>(`/api/sessions${suffix}`);
  return payload.sessions;
}

export async function fetchSessionDetail(sessionID: string): Promise<SessionDetail> {
  return requestJSON<SessionDetail>(`/api/sessions/${encodeURIComponent(sessionID)}`);
}

export async function fetchSessionEvents(sessionID: string): Promise<EventPage> {
  const encoded = encodeURIComponent(sessionID);
  const allEvents: EventPage['events'] = [];
  let afterSeq = -1;

  while (true) {
    const page = await requestJSON<EventPage>(
      `/api/sessions/${encoded}/events?limit=500&after_seq=${afterSeq}`,
    );
    allEvents.push(...page.events);
    if (!page.has_more) {
      return { events: allEvents, has_more: false, next_after_seq: page.next_after_seq };
    }
    afterSeq = page.next_after_seq!;
  }
}

export async function fetchViewerStatus(runtimeID?: string): Promise<ViewerStatus> {
  const suffix = runtimeID ? `?runtime_id=${encodeURIComponent(runtimeID)}` : '';
  return requestJSON<ViewerStatus>(`/api/status${suffix}`);
}

export async function switchRuntimeWorkspace(
  runtimeID: string,
  workspaceID: string,
): Promise<{ session_id?: string; workspace_id?: string }> {
  return requestJSON<{ session_id?: string; workspace_id?: string }>(
    `/api/runtimes/${encodeURIComponent(runtimeID)}/workspaces/${encodeURIComponent(workspaceID)}/switch`,
    { method: 'POST' },
  );
}
