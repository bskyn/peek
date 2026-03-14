import type { EventPage, SessionDetail, SessionSummary, ViewerStatus } from './types';

async function requestJSON<T>(input: string): Promise<T> {
  const response = await fetch(input);
  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || `request failed: ${response.status}`);
  }
  return (await response.json()) as T;
}

export async function fetchSessions(): Promise<SessionSummary[]> {
  const payload = await requestJSON<{ sessions: SessionSummary[] }>('/api/sessions');
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

export async function fetchViewerStatus(): Promise<ViewerStatus> {
  return requestJSON<ViewerStatus>('/api/status');
}
