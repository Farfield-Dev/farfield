export type Json = null | boolean | number | string | Json[] | { [key: string]: Json };

export interface Scope {
  conversationId?: string;
  traceId?: string;
  spanId?: string;
  parentId?: string;
  agent?: string;
  tags?: Record<string, string>;
}

export interface Event extends Scope {
  kind: string;
  content: Json;
  id?: string;
  occurredAt?: Date | string;
  sequence?: number;
  tool?: string;
  status?: string;
}

export interface WireEvent {
  id: string;
  conversation_id: string;
  kind: string;
  content: Json;
  occurred_at: string;
  sequence?: number;
  trace_id?: string;
  span_id?: string;
  parent_id?: string;
  agent?: string;
  tool?: string;
  status?: string;
  tags: Record<string, string>;
}

export interface ContentRef {
  sha256: string;
  size: number;
  media_type: string;
  key: string;
  storage?: string;
  entry_index?: number;
}

export interface HistoryRecord {
  schema_version: string;
  id: string;
  conversation_id: string;
  kind: string;
  occurred_at: string;
  recorded_at: string;
  sequence: number | null;
  trace_id: string | null;
  span_id: string | null;
  parent_id: string | null;
  agent: string | null;
  tool: string | null;
  status: string | null;
  tags: Record<string, string>;
  content: ContentRef;
  record_sha256: string;
}

export interface Entry {
  record: HistoryRecord;
  content: Json;
}

export interface Segment {
  schema_version: string;
  id: string;
  conversation_id: string;
  shard: string;
  created_at: string;
  entries: Entry[];
  segment_sha256: string;
}

export interface ConversationSummary {
  id: string;
  record_count: number;
  first_seen_at: string;
  last_seen_at: string;
  agents: string[];
  kinds: string[];
}

export interface Query {
  conversationId?: string;
  traceId?: string;
  kind?: string;
  agent?: string;
  tool?: string;
  status?: string;
  since?: Date | string;
  limit?: number;
}

export type RunStatus =
  | "queued"
  | "running"
  | "waiting"
  | "sleeping"
  | "completed"
  | "failed"
  | "cancelled"
  | "ambiguous";

export interface RuntimeEvent {
  schema_version: string;
  id: string;
  run_id: string;
  operation_id: string;
  sequence: number;
  attempt: number;
  kind: string;
  from: RunStatus | null;
  to: RunStatus;
  occurred_at: string;
  recorded_at: string;
  checkpoint?: Json;
  previous_event_sha256?: string;
  event_sha256: string;
}

export interface Run {
  id: string;
  status: RunStatus;
  sequence: number;
  attempt: number;
  updated_at: string;
  last_event_id: string;
  last_event_sha256: string;
  checkpoint?: Json;
  checkpoint_at?: string;
}

export interface RequestOptions {
  signal?: AbortSignal;
}
