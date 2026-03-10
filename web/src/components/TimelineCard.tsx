import type { ViewerEvent } from '../lib/types';
import {
  titleForEvent,
  describeEvent,
  extraPayload,
  formatDateTime,
  usageSummary,
} from '../lib/format';
import { DiffBlock } from './DiffBlock';

function isUserEvent(type: string): boolean {
  return type === 'user_message';
}

function headerColor(type: string): string {
  switch (type) {
    case 'user_message':
      return 'text-peach';
    case 'assistant_message':
      return 'text-blue';
    case 'assistant_thinking':
      return 'text-lavender';
    case 'tool_call':
    case 'tool_result':
      return 'text-sapphire';
    case 'error':
      return 'text-red';
    default:
      return 'text-overlay-1';
  }
}

type DiffInfo =
  | { kind: 'diff'; diffText: string; filePath: string; operation: string }
  | { kind: 'delete'; filePath: string };

function asRecord(value: unknown): Record<string, unknown> | null {
  if (value == null || typeof value !== 'object') return null;
  return value as Record<string, unknown>;
}

function stringProp(record: Record<string, unknown> | null, key: string): string {
  if (record == null) return '';
  const value = record[key];
  return typeof value === 'string' ? value : '';
}

function extractDiffInfo(event: ViewerEvent): DiffInfo | null {
  if (event.type !== 'tool_call') return null;

  const toolName =
    typeof event.payload_json.tool_name === 'string' ? event.payload_json.tool_name : '';
  const payload = asRecord(event.payload_json);

  // Codex apply_patch: raw unified diff or delete operation
  if (toolName === 'apply_patch' || toolName.endsWith('.apply_patch')) {
    const input = asRecord(payload?.input) ?? payload;
    const diff = stringProp(input, 'diff');
    const filePath = stringProp(input, 'file_path');
    const operation = stringProp(input, 'operation');

    if (operation === 'delete') {
      return { kind: 'delete', filePath };
    }
    if (diff !== '') {
      return { kind: 'diff', diffText: ensureUnifiedHeaders(diff), filePath, operation };
    }
    return null;
  }

  // Claude Edit/Write: old_string/new_string or content
  if (
    toolName === 'Edit' ||
    toolName === 'edit_file' ||
    toolName === 'Write' ||
    toolName === 'write_file'
  ) {
    const rec = asRecord(payload?.input);
    if (rec == null) return null;
    const filePath =
      typeof rec.file_path === 'string'
        ? rec.file_path
        : typeof rec.path === 'string'
          ? rec.path
          : '';

    if (typeof rec.old_string === 'string' && typeof rec.new_string === 'string') {
      return { kind: 'diff', diffText: '', filePath, operation: 'edit' };
    }
    if (typeof rec.content === 'string') {
      return { kind: 'diff', diffText: '', filePath, operation: 'create' };
    }
  }

  return null;
}

// Extract old_string/new_string for Claude edits (DiffBlock builds its own unified diff)
function extractEditStrings(event: ViewerEvent): { oldText: string; newText: string } | null {
  const rec = asRecord(asRecord(event.payload_json)?.input);
  if (rec == null) return null;
  if (typeof rec.old_string === 'string' && typeof rec.new_string === 'string') {
    return { oldText: rec.old_string, newText: rec.new_string };
  }
  if (typeof rec.content === 'string') {
    return { oldText: '', newText: rec.content };
  }
  return null;
}

// Ensure raw diff has unified headers so parseDiff can handle it
function ensureUnifiedHeaders(diff: string): string {
  if (diff.startsWith('---')) return diff;
  return `--- a\n+++ b\n${diff}`;
}

export function TimelineCard({ event }: { event: ViewerEvent }) {
  const title = titleForEvent(event);
  const description = describeEvent(event);
  const extra = extraPayload(event);
  const isUser = isUserEvent(event.type);
  const diffInfo = extractDiffInfo(event);
  const color = headerColor(event.type);
  const usage = usageSummary(event);

  return (
    <article
      className={`rounded-lg border p-3 ${
        isUser ? 'border-surface-0 bg-mantle' : 'border-border bg-crust/50'
      }`}
    >
      <div className="flex items-baseline justify-between gap-3">
        <div className={`flex items-baseline gap-1.5 ${color}`}>
          <span className="text-[10px] font-medium uppercase tracking-wider">{title}</span>
          <span className="font-mono text-[11px]">#{event.seq}</span>
        </div>
        <div className="flex shrink-0 items-center gap-2 text-[10px] tabular-nums text-overlay-0">
          {usage != null ? (
            <>
              <span className="text-yellow">
                Token Count: <span className="font-semibold">{usage.tokenCount}</span>
              </span>
              {usage.cost != null ? (
                <>
                  <span aria-hidden="true" className="text-overlay-1">
                    |
                  </span>
                  <span className="text-green">
                    Cost: <span className="font-semibold">{usage.cost}</span>
                  </span>
                </>
              ) : null}
              <span aria-hidden="true" className="text-overlay-1">
                |
              </span>
            </>
          ) : null}
          <span>{formatDateTime(event.timestamp)}</span>
        </div>
      </div>

      {diffInfo != null ? (
        <div className="mt-2 overflow-hidden rounded-md">
          {diffInfo.filePath !== '' ? (
            <div className="flex items-center gap-2 bg-crust px-2.5 py-1 font-mono text-[10px] text-overlay-1">
              <span>{diffInfo.filePath}</span>
              {diffInfo.kind === 'delete' ? (
                <span className="rounded-sm bg-red/15 px-1.5 py-0.5 text-[9px] uppercase text-red">
                  deleted
                </span>
              ) : 'operation' in diffInfo && diffInfo.operation !== '' ? (
                <span className="rounded-sm bg-surface-0 px-1.5 py-0.5 text-[9px] uppercase text-overlay-0">
                  {diffInfo.operation}
                </span>
              ) : null}
            </div>
          ) : null}
          {diffInfo.kind === 'delete' ? (
            <div className="bg-red/5 px-2.5 py-2 font-mono text-[11px] text-red/80">
              File deleted
            </div>
          ) : diffInfo.diffText !== '' ? (
            // Codex raw diff or pre-formatted diff — parse directly
            <DiffBlock rawDiff={diffInfo.diffText} />
          ) : (
            // Claude edit — build diff from old/new strings
            <DiffBlock editStrings={extractEditStrings(event)} />
          )}
        </div>
      ) : description !== '' ? (
        <pre
          className={`mt-2 max-h-80 overflow-auto whitespace-pre-wrap wrap-break-word rounded-md p-2.5 font-mono text-[11px] leading-relaxed ${
            isUser ? 'bg-crust text-subtext-1' : 'bg-mantle text-subtext-0'
          }`}
        >
          {description}
        </pre>
      ) : null}

      {extra !== '' ? (
        <details className="mt-2">
          <summary className="cursor-pointer text-[11px] text-blue hover:text-sapphire">
            Expand payload
          </summary>
          <pre className="mt-1.5 max-h-60 overflow-auto whitespace-pre-wrap wrap-break-word rounded-md bg-mantle p-2.5 font-mono text-[10px] leading-relaxed text-overlay-1">
            {extra}
          </pre>
        </details>
      ) : null}
    </article>
  );
}
