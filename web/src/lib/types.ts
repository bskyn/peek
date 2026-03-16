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
};

export type LiveEnvelope =
  | {
      type: 'session_upsert';
      session?: SessionSummary;
    }
  | {
      type: 'event_append';
      event?: ViewerEvent;
    }
  | {
      type: 'active_session';
      active_session_id?: string;
    }
  | {
      type: 'runtime_status';
      runtime?: RuntimeStatus;
    };

export type StreamStatus = 'connecting' | 'live' | 'retrying' | 'disconnected';

export type ViewerStatus = {
  active_session_id: string;
  runtime?: RuntimeStatus;
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
