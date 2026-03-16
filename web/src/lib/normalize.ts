import type {
  LiveEnvelope,
  ManagedRuntimeView,
  RuntimeStatus,
  RuntimeWorkspaceView,
  SessionDetail,
  SessionSummary,
  ViewerEvent,
  ViewerStatus,
} from './types';

type JsonRecord = Record<string, unknown>;

const EVENT_TYPES = [
  'user_message',
  'assistant_thinking',
  'assistant_message',
  'tool_call',
  'tool_result',
  'progress',
  'system',
  'error',
] as const;

const RUNTIME_PHASES = [
  'idle',
  'materializing',
  'bootstrapping',
  'starting',
  'ready',
  'failed',
] as const;

const BOOTSTRAP_STATUSES = ['pending', 'running', 'succeeded', 'failed'] as const;
const COMPANION_SERVICE_STATUSES = ['starting', 'ready', 'failed', 'stopped'] as const;
const MANAGED_RUNTIME_STATUSES = ['running', 'stopped'] as const;
const WORKSPACE_STATUSES = ['active', 'frozen', 'merge_pending', 'conflict', 'merged'] as const;

function asRecord(value: unknown): JsonRecord | null {
  if (value == null || typeof value !== 'object' || Array.isArray(value)) {
    return null;
  }
  return value as JsonRecord;
}

function asString(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function asNumber(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

function asBoolean(value: unknown): boolean {
  return value === true;
}

function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

function asEnum<T extends string>(value: unknown, allowed: readonly T[], fallback: T): T {
  return typeof value === 'string' && allowed.includes(value as T) ? (value as T) : fallback;
}

export function normalizeSessionSummary(value: unknown): SessionSummary | null {
  const record = asRecord(value);
  if (record == null) return null;

  const id = asString(record.id);
  if (id === '') return null;

  const parentSessionID = asString(record.parent_session_id);
  return {
    id,
    source: asString(record.source),
    project_path: asString(record.project_path),
    source_session_id: asString(record.source_session_id),
    parent_session_id: parentSessionID === '' ? undefined : parentSessionID,
    created_at: asString(record.created_at),
    updated_at: asString(record.updated_at),
    event_count: asNumber(record.event_count),
  };
}

export function normalizeSessionDetail(value: unknown): SessionDetail {
  const record = asRecord(value);
  if (record == null) {
    throw new Error('invalid session detail payload');
  }

  const session = normalizeSessionSummary(record.session);
  if (session == null) {
    throw new Error('invalid session detail payload');
  }

  const rootSession = normalizeSessionSummary(record.root_session) ?? session;
  const childSessions = asArray(record.child_sessions)
    .map(normalizeSessionSummary)
    .filter((entry): entry is SessionSummary => entry != null);

  return {
    session,
    root_session: rootSession,
    child_sessions: childSessions,
  };
}

export function normalizeViewerEvent(value: unknown): ViewerEvent | null {
  const record = asRecord(value);
  if (record == null) return null;

  const id = asString(record.id);
  const sessionID = asString(record.session_id);
  if (id === '' || sessionID === '') return null;

  const parentEventID = asString(record.parent_event_id);
  return {
    id,
    session_id: sessionID,
    timestamp: asString(record.timestamp),
    seq: asNumber(record.seq),
    type: asEnum(record.type, EVENT_TYPES, 'system'),
    role: asString(record.role) || undefined,
    parent_event_id: parentEventID === '' ? undefined : parentEventID,
    payload_json: asRecord(record.payload_json) ?? {},
  };
}

export function normalizeViewerStatus(value: unknown): ViewerStatus {
  const record = asRecord(value);
  if (record == null) {
    throw new Error('invalid viewer status payload');
  }

  return {
    current_runtime_id: asString(record.current_runtime_id),
    active_session_id: asString(record.active_session_id),
    runtime: normalizeRuntimeStatus(record.runtime),
    runtimes: asArray(record.runtimes)
      .map(normalizeManagedRuntimeView)
      .filter((entry): entry is ManagedRuntimeView => entry != null),
    workspaces: asArray(record.workspaces)
      .map(normalizeRuntimeWorkspaceView)
      .filter((entry): entry is RuntimeWorkspaceView => entry != null),
  };
}

export function normalizeLiveEnvelope(value: unknown): LiveEnvelope | null {
  const record = asRecord(value);
  if (record == null) return null;

  const runtimeID = asString(record.runtime_id) || undefined;
  switch (record.type) {
    case 'session_upsert':
      return {
        type: 'session_upsert',
        runtime_id: runtimeID,
        session: normalizeSessionSummary(record.session) ?? undefined,
      };
    case 'event_append':
      return {
        type: 'event_append',
        runtime_id: runtimeID,
        event: normalizeViewerEvent(record.event) ?? undefined,
      };
    case 'active_session':
      return {
        type: 'active_session',
        runtime_id: runtimeID,
        active_session_id: asString(record.active_session_id) || undefined,
      };
    case 'runtime_status':
      return {
        type: 'runtime_status',
        runtime_id: runtimeID,
        runtime: normalizeRuntimeStatus(record.runtime),
      };
    default:
      return null;
  }
}

function normalizeRuntimeStatus(value: unknown): RuntimeStatus | undefined {
  const record = asRecord(value);
  if (record == null) return undefined;

  const bootstrapRecord = asRecord(record.bootstrap);
  const browserRecord = asRecord(record.browser);

  return {
    enabled: asBoolean(record.enabled),
    config_source: asString(record.config_source) || undefined,
    active_workspace_id: asString(record.active_workspace_id) || undefined,
    phase: asEnum(record.phase, RUNTIME_PHASES, 'idle'),
    message: asString(record.message) || undefined,
    bootstrap: {
      status: asEnum(bootstrapRecord?.status, BOOTSTRAP_STATUSES, 'pending'),
      fingerprint: asString(bootstrapRecord?.fingerprint) || undefined,
      reused: asBoolean(bootstrapRecord?.reused) || undefined,
      last_error: asString(bootstrapRecord?.last_error) || undefined,
    },
    services: asArray(record.services)
      .map(normalizeRuntimeService)
      .filter((entry): entry is RuntimeStatus['services'][number] => entry != null),
    browser: {
      path_prefix: asString(browserRecord?.path_prefix),
      target_url: asString(browserRecord?.target_url) || undefined,
    },
    updated_at: asString(record.updated_at),
  };
}

function normalizeRuntimeService(value: unknown): RuntimeStatus['services'][number] | null {
  const service = asRecord(value);
  if (service == null) return null;

  const name = asString(service.name);
  if (name === '') return null;

  return {
    name,
    role: asString(service.role),
    status: asEnum(service.status, COMPANION_SERVICE_STATUSES, 'stopped'),
    target_url: asString(service.target_url) || undefined,
    last_error: asString(service.last_error) || undefined,
  };
}

function normalizeManagedRuntimeView(value: unknown): ManagedRuntimeView | null {
  const record = asRecord(value);
  if (record == null) return null;

  const runtime = asRecord(record.runtime);
  if (runtime == null) return null;

  const runtimeID = asString(runtime.id);
  if (runtimeID === '') return null;

  const checkout = asRecord(record.checkout);
  const companion = asRecord(record.companion);

  return {
    runtime: {
      id: runtimeID,
      project_path: asString(runtime.project_path),
      root_workspace_id: asString(runtime.root_workspace_id),
      active_workspace_id: asString(runtime.active_workspace_id),
      active_session_id: asString(runtime.active_session_id),
      source: asString(runtime.source),
      status: asEnum(runtime.status, MANAGED_RUNTIME_STATUSES, 'stopped'),
      heartbeat_at: asString(runtime.heartbeat_at),
      created_at: asString(runtime.created_at),
      updated_at: asString(runtime.updated_at),
    },
    checkout:
      checkout == null
        ? undefined
        : {
            checkout_path: asString(checkout.checkout_path),
            runtime_id: asString(checkout.runtime_id),
            workspace_id: asString(checkout.workspace_id),
            claimed_at: asString(checkout.claimed_at),
            updated_at: asString(checkout.updated_at),
          },
    companion:
      companion == null
        ? undefined
        : {
            runtime_id: asString(companion.runtime_id),
            active_workspace_id: asString(companion.active_workspace_id),
            owner_session_id: asString(companion.owner_session_id),
            config_source: asString(companion.config_source),
            phase: asString(companion.phase),
            message: asString(companion.message),
            browser_path_prefix: asString(companion.browser_path_prefix),
            browser_target_url: asString(companion.browser_target_url),
            updated_at: asString(companion.updated_at),
          },
  };
}

function normalizeRuntimeWorkspaceView(value: unknown): RuntimeWorkspaceView | null {
  const record = asRecord(value);
  if (record == null) return null;

  const workspace = asRecord(record.workspace);
  if (workspace == null) return null;

  const workspaceID = asString(workspace.id);
  if (workspaceID === '') return null;

  const parentWorkspaceID = asString(workspace.parent_workspace_id);
  return {
    workspace: {
      id: workspaceID,
      parent_workspace_id: parentWorkspaceID === '' ? undefined : parentWorkspaceID,
      status: asEnum(workspace.status, WORKSPACE_STATUSES, 'active'),
      project_path: asString(workspace.project_path),
      worktree_path: asString(workspace.worktree_path) || undefined,
      git_ref: asString(workspace.git_ref) || undefined,
      branch_from_seq:
        typeof workspace.branch_from_seq === 'number' ? workspace.branch_from_seq : undefined,
      sibling_ordinal: asNumber(workspace.sibling_ordinal),
      session_count: asNumber(workspace.session_count),
      checkpoint_count: asNumber(workspace.checkpoint_count),
      created_at: asString(workspace.created_at),
      updated_at: asString(workspace.updated_at),
    },
    is_active: asBoolean(record.is_active),
    latest_session: normalizeSessionSummary(record.latest_session) ?? undefined,
    runtime_app_path: asString(record.runtime_app_path) || undefined,
  };
}
