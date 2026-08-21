import { createHash } from "node:crypto";
import type {
  HookCallback,
  HookCallbackMatcher,
  HookEvent,
  HookInput,
} from "@anthropic-ai/claude-agent-sdk";

import { Farfield } from "../client.js";
import { BackgroundProcessor, type BackgroundProcessorOptions, type ProcessorStats } from "../processor.js";
import type { Event, Json } from "../types.js";

export const CLAUDE_AGENT_HOOK_EVENTS = [
  "PreToolUse",
  "PostToolUse",
  "PostToolUseFailure",
  "PostToolBatch",
  "Notification",
  "UserPromptSubmit",
  "UserPromptExpansion",
  "SessionStart",
  "SessionEnd",
  "Stop",
  "StopFailure",
  "SubagentStart",
  "SubagentStop",
  "PreCompact",
  "PostCompact",
  "PermissionRequest",
  "PermissionDenied",
  "Setup",
  "TeammateIdle",
  "TaskCreated",
  "TaskCompleted",
  "Elicitation",
  "ElicitationResult",
  "ConfigChange",
  "WorktreeCreate",
  "WorktreeRemove",
  "InstructionsLoaded",
  "CwdChanged",
  "FileChanged",
  "DirectoryAdded",
  "MessageDisplay",
] as const satisfies readonly HookEvent[];

export type ClaudeAgentHookEvent = HookEvent;
export type ClaudeAgentHookInput = HookInput;
export type ClaudeAgentHookCallback = HookCallback;
export type ClaudeAgentHookMatcher = HookCallbackMatcher;

/** Non-blocking Farfield capture hooks for `@anthropic-ai/claude-agent-sdk`. */
export class FarfieldClaudeAgentHooks {
  readonly processor: BackgroundProcessor;
  readonly #defaultAgent: string | undefined;

  constructor(
    client: Farfield,
    options: BackgroundProcessorOptions & { processor?: BackgroundProcessor; defaultAgent?: string } = {},
  ) {
    this.processor = options.processor ?? new BackgroundProcessor(client, options);
    this.#defaultAgent = options.defaultAgent;
  }

  matchers(options: { timeout?: number } = {}): Partial<Record<HookEvent, HookCallbackMatcher[]>> {
    return Object.fromEntries(
      CLAUDE_AGENT_HOOK_EVENTS.map((event) => [
        event,
        [{ hooks: [this.capture], ...(options.timeout === undefined ? {} : { timeout: options.timeout }) }],
      ]),
    ) as Partial<Record<HookEvent, HookCallbackMatcher[]>>;
  }

  capture: HookCallback = async (input, callbackToolUseId, options) => {
    options.signal.throwIfAborted();
    await this.processor.submit(eventFromHook(input, callbackToolUseId, this.#defaultAgent));
    return {};
  };

  flush(): Promise<boolean> {
    return this.processor.flush();
  }

  shutdown(): Promise<boolean> {
    return this.processor.shutdown();
  }

  stats(): ProcessorStats {
    return this.processor.stats();
  }
}

function eventFromHook(input: HookInput, callbackToolUseId: string | undefined, defaultAgent?: string): Event {
  const payload = input as unknown as Record<string, unknown>;
  const eventName = input.hook_event_name;
  if (!input.session_id) throw new TypeError("farfield: Claude Agent hook input is missing session_id");
  const toolUseId = stringValue(payload.tool_use_id) ?? callbackToolUseId;
  const agentId = stringValue(payload.agent_id);
  const spanId = safeOptionalId(toolUseId ?? agentId, "span_claude_");
  const parentId = toolUseId ? safeOptionalId(agentId, "span_claude_") : undefined;
  const agent = stringValue(payload.agent_type) ?? defaultAgent;
  const tool = stringValue(payload.tool_name);
  return {
    conversationId: externalId(input.session_id, "conv_claude_"),
    kind: hookKind(eventName),
    content: {
      schema: "farfield.claude_agent_sdk.hook.v1",
      hook: jsonValue(payload),
      callback_tool_use_id: callbackToolUseId ?? null,
    },
    ...(validId(input.session_id) ? { traceId: input.session_id } : {}),
    ...(spanId ? { spanId } : {}),
    ...(parentId ? { parentId } : {}),
    ...(agent ? { agent } : {}),
    ...(tool ? { tool } : {}),
    ...(hookStatus(eventName) ? { status: hookStatus(eventName)! } : {}),
    tags: {
      "farfield.source": "claude-agent-sdk",
      "claude.hook.event": eventName.slice(0, 1024),
    },
  };
}

function hookKind(event: string): string {
  const kinds: Record<string, string> = {
    UserPromptSubmit: "message.user",
    UserPromptExpansion: "message.expansion",
    PreToolUse: "tool.call",
    PostToolUse: "tool.result",
    PostToolUseFailure: "tool.result",
    PostToolBatch: "tool.batch",
    PermissionRequest: "tool.permission",
    PermissionDenied: "tool.permission",
    SessionStart: "conversation.session.start",
    SessionEnd: "conversation.session.end",
    Stop: "agent.stop",
    StopFailure: "agent.stop",
    SubagentStart: "agent.subagent.start",
    SubagentStop: "agent.subagent.stop",
    PreCompact: "conversation.compact",
    PostCompact: "conversation.compacted",
    Notification: "agent.notification",
    Setup: "agent.setup",
    TeammateIdle: "agent.teammate.idle",
    TaskCreated: "agent.task.created",
    TaskCompleted: "agent.task.completed",
    Elicitation: "agent.elicitation",
    ElicitationResult: "agent.elicitation.result",
    ConfigChange: "agent.config",
    WorktreeCreate: "workspace.worktree.created",
    WorktreeRemove: "workspace.worktree.removed",
    InstructionsLoaded: "agent.instructions",
    CwdChanged: "workspace.cwd",
    FileChanged: "workspace.file",
    DirectoryAdded: "workspace.directory",
    MessageDisplay: "message.display",
  };
  return kinds[event] ?? "agent.hook";
}

function hookStatus(event: string): string | undefined {
  const statuses: Record<string, string> = {
    PreToolUse: "running",
    PostToolUse: "ok",
    PostToolUseFailure: "error",
    PermissionDenied: "denied",
    SessionStart: "running",
    SessionEnd: "ok",
    Stop: "ok",
    StopFailure: "error",
    SubagentStart: "running",
    SubagentStop: "ok",
    TaskCreated: "running",
    TaskCompleted: "ok",
  };
  return statuses[event];
}

function safeOptionalId(value: string | undefined, prefix: string): string | undefined {
  return value === undefined ? undefined : externalId(value, prefix);
}

function externalId(value: string, prefix: string): string {
  return validId(value) ? value : prefix + sha256(value);
}

function validId(value: string): boolean {
  return value.length > 0 && value.length <= 255 && /^[A-Za-z0-9][A-Za-z0-9._:@/-]*$/.test(value);
}

function stringValue(value: unknown): string | undefined {
  return typeof value === "string" && value.length > 0 ? value : undefined;
}

function sha256(value: string): string {
  return createHash("sha256").update(value).digest("hex");
}

function jsonValue(value: unknown): Json {
  if (value === null || typeof value === "boolean" || typeof value === "string") return value;
  if (typeof value === "number") return Number.isFinite(value) ? value : String(value);
  if (Array.isArray(value)) return value.map(jsonValue);
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, jsonValue(item)]));
  }
  return String(value);
}
