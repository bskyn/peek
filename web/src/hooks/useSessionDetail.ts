import { useEffect, useRef, useState } from "react";

import { fetchSessionDetail, fetchSessionEvents } from "../lib/api";
import { openStream } from "../lib/stream";
import type { LiveEnvelope, SessionDetail, StreamStatus, ViewerEvent } from "../lib/types";

const AUTO_SCROLL_THRESHOLD = 96;
export type TimelineSort = "asc" | "desc";

function mergeEvents(base: ViewerEvent[], incoming: ViewerEvent[]): ViewerEvent[] {
  const bySeq = new Map<number, ViewerEvent>();
  for (const e of base) bySeq.set(e.seq, e);
  for (const e of incoming) bySeq.set(e.seq, e);
  return Array.from(bySeq.values()).sort((a, b) => a.seq - b.seq);
}

function appendUniqueEvent(current: ViewerEvent[], next: ViewerEvent): ViewerEvent[] {
  if (current.some((e) => e.id === next.id || e.seq === next.seq)) return current;
  return [...current, next].sort((a, b) => a.seq - b.seq);
}

function sortTimelineEvents(events: ViewerEvent[], direction: TimelineSort): ViewerEvent[] {
  const sorted = [...events].sort((a, b) => a.seq - b.seq);
  return direction === "asc" ? sorted : sorted.reverse();
}

export function useSessionDetail(selectedSessionID: string) {
  const [detail, setDetail] = useState<SessionDetail | null>(null);
  const [events, setEvents] = useState<ViewerEvent[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [totalCount, setTotalCount] = useState(0);
  const [error, setError] = useState("");
  const [isLoading, setIsLoading] = useState(false);
  const [streamStatus, setStreamStatus] = useState<StreamStatus>("disconnected");
  const [timelineSort, setTimelineSort] = useState<TimelineSort>("asc");

  const timelineRef = useRef<HTMLDivElement | null>(null);
  const stickToBottomRef = useRef(true);

  // Fetch detail + events on session change
  useEffect(() => {
    if (selectedSessionID === "") {
      setDetail(null);
      setEvents([]);
      setHasMore(false);
      setTotalCount(0);
      setError("");
      setStreamStatus("disconnected");
      return;
    }

    // Clear stale data from previous session immediately
    setDetail(null);
    setEvents([]);
    setHasMore(false);
    setTotalCount(0);
    setError("");

    let cancelled = false;
    setIsLoading(true);

    Promise.all([fetchSessionDetail(selectedSessionID), fetchSessionEvents(selectedSessionID)])
      .then(([nextDetail, page]) => {
        if (cancelled) return;
        setDetail(nextDetail);
        setTotalCount(nextDetail.session.event_count);
        setHasMore(page.has_more);
        // Merge with any events that arrived via SSE during the fetch
        setEvents((current) => mergeEvents(page.events, current));
        setError("");
        stickToBottomRef.current = true;
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setDetail(null);
        setEvents([]);
        setHasMore(false);
        setTotalCount(0);
        setError(err instanceof Error ? err.message : "Unknown error");
      })
      .finally(() => {
        if (!cancelled) setIsLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [selectedSessionID]);

  // Live stream for events
  useEffect(() => {
    if (selectedSessionID === "") return;

    return openStream(
      `session_id=${encodeURIComponent(selectedSessionID)}`,
      (envelope: LiveEnvelope) => {
        if (envelope.type !== "event_append" || envelope.event == null) return;
        setEvents((cur) => appendUniqueEvent(cur, envelope.event!));
      },
      setStreamStatus,
    );
  }, [selectedSessionID]);

  // Scroll stickiness tracking
  useEffect(() => {
    const node = timelineRef.current;
    if (node == null) return;

    const updateStickiness = () => {
      if (timelineSort === "desc") {
        // In desc mode, "sticky" means pinned to the top (newest first)
        stickToBottomRef.current = node.scrollTop < AUTO_SCROLL_THRESHOLD;
      } else {
        const dist = node.scrollHeight - node.scrollTop - node.clientHeight;
        stickToBottomRef.current = dist < AUTO_SCROLL_THRESHOLD;
      }
    };

    updateStickiness();
    node.addEventListener("scroll", updateStickiness);
    return () => node.removeEventListener("scroll", updateStickiness);
  }, [selectedSessionID, timelineSort]);

  // Auto-scroll on new events
  useEffect(() => {
    const node = timelineRef.current;
    if (node == null || !stickToBottomRef.current) return;
    if (timelineSort === "desc") {
      node.scrollTop = 0;
      return;
    }
    node.scrollTop = node.scrollHeight;
  }, [events.length, selectedSessionID, timelineSort]);

  const displayedEvents = sortTimelineEvents(events, timelineSort);

  return {
    detail,
    setDetail,
    events,
    displayedEvents,
    error,
    isLoading,
    streamStatus,
    timelineSort,
    setTimelineSort,
    timelineRef,
    hasMore,
    totalCount,
  };
}
