export { APIError, DroppedEvent, FarfieldError, TransportError } from "./errors.js";
export { BackgroundProcessor, type BackgroundProcessorOptions, type ProcessorStats } from "./processor.js";
export {
  FarfieldOpenAIAgentsExporter,
  type OpenAIAgentsExporterStats,
  type OpenAIAgentsTraceItem,
} from "./integrations/openai-agents.js";
export {
  CLAUDE_AGENT_HOOK_EVENTS,
  FarfieldClaudeAgentHooks,
  type ClaudeAgentHookCallback,
  type ClaudeAgentHookEvent,
  type ClaudeAgentHookInput,
  type ClaudeAgentHookMatcher,
} from "./integrations/claude-agent-sdk.js";
export {
  Batch,
  Conversation,
  Farfield,
  VERSION,
  type ConversationOptions,
  type FarfieldOptions,
} from "./client.js";
export type {
  ContentRef,
  ConversationSummary,
  Entry,
  Event,
  HistoryRecord,
  Json,
  Query,
  RequestOptions,
  SearchHit,
  SearchQuery,
  SearchResult,
  Scope,
  Segment,
  WireEvent,
} from "./types.js";
