export type SessionSummary = {
  id: string;
  source: string;
  project_path: string;
  source_session_id: string;
  parent_session_id?: string;
  created_at: string;
  updated_at: string;
  event_count: number;
};

export type SessionDetail = {
  session: SessionSummary;
  root_session: SessionSummary;
  child_sessions: SessionSummary[];
};

export type ViewerEvent = {
  id: string;
  session_id: string;
  timestamp: string;
  seq: number;
  type:
    | 'user_message'
    | 'assistant_thinking'
    | 'assistant_message'
    | 'tool_call'
    | 'tool_result'
    | 'progress'
    | 'system'
    | 'error';
  role?: string;
  parent_event_id?: string;
  payload_json: Record<string, unknown>;
};

export type EventPage = {
  events: ViewerEvent[];
  has_more: boolean;
  next_after_seq?: number;
  next_before_seq?: number;
};

export type LiveEnvelope =
  | {
      type: 'session_upsert';
      runtime_id?: string;
      session?: SessionSummary;
    }
  | {
      type: 'event_append';
      runtime_id?: string;
      event?: ViewerEvent;
    }
  | {
      type: 'active_session';
      runtime_id?: string;
      active_session_id?: string;
    }
  | {
      type: 'runtime_status';
      runtime_id?: string;
      runtime?: RuntimeStatus;
    };

export type StreamStatus = 'connecting' | 'live' | 'retrying' | 'disconnected';

export type ViewerStatus = {
  current_runtime_id: string;
  active_session_id: string;
  runtime?: RuntimeStatus;
  runtimes: ManagedRuntimeView[];
  workspaces: RuntimeWorkspaceView[];
};

export type BootstrapStatus = 'pending' | 'running' | 'succeeded' | 'failed';

export type CompanionServiceStatus = 'starting' | 'ready' | 'failed' | 'stopped';

export type RuntimeStatus = {
  enabled: boolean;
  config_source?: string;
  active_workspace_id?: string;
  phase: 'idle' | 'materializing' | 'bootstrapping' | 'starting' | 'ready' | 'failed';
  message?: string;
  bootstrap: {
    status: BootstrapStatus;
    fingerprint?: string;
    reused?: boolean;
    last_error?: string;
  };
  services: Array<{
    name: string;
    role: string;
    status: CompanionServiceStatus;
    target_url?: string;
    last_error?: string;
  }>;
  browser: {
    path_prefix: string;
    target_url?: string;
  };
  updated_at: string;
};

export type ManagedRuntimeView = {
  runtime: {
    id: string;
    project_path: string;
    root_workspace_id: string;
    active_workspace_id: string;
    active_session_id: string;
    source: string;
    status: 'running' | 'stopped';
    heartbeat_at: string;
    created_at: string;
    updated_at: string;
  };
  checkout?: {
    checkout_path: string;
    runtime_id: string;
    workspace_id: string;
    claimed_at: string;
    updated_at: string;
  };
  companion?: {
    runtime_id: string;
    active_workspace_id: string;
    owner_session_id: string;
    config_source: string;
    phase: string;
    message: string;
    browser_path_prefix: string;
    browser_target_url: string;
    updated_at: string;
  };
};

export type RuntimeWorkspaceView = {
  workspace: {
    id: string;
    parent_workspace_id?: string;
    status: 'active' | 'frozen' | 'merge_pending' | 'conflict' | 'merged';
    project_path: string;
    worktree_path?: string;
    git_ref?: string;
    branch_from_seq?: number;
    sibling_ordinal: number;
    session_count: number;
    checkpoint_count: number;
    created_at: string;
    updated_at: string;
  };
  is_active: boolean;
  latest_session?: SessionSummary;
  runtime_app_path?: string;
};
