import type { LiveEnvelope, StreamStatus } from './types';

export function openStream(
  query: string,
  onEnvelope: (envelope: LiveEnvelope) => void,
  onStatus: (status: StreamStatus) => void,
): () => void {
  const target = query === '' ? '/api/stream' : `/api/stream?${query}`;
  const source = new EventSource(target);

  onStatus('connecting');

  source.onopen = () => {
    onStatus('live');
  };

  source.onmessage = (event) => {
    try {
      onEnvelope(JSON.parse(event.data) as LiveEnvelope);
    } catch {
      // Ignore malformed envelopes.
    }
  };

  source.onerror = () => {
    onStatus('retrying');
  };

  return () => {
    onStatus('disconnected');
    source.close();
  };
}
