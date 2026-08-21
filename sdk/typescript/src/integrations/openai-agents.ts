import { createHash } from "node:crypto";

import { Farfield } from "../client.js";
import type { Event, Json } from "../types.js";

export interface OpenAIAgentsTraceItem {
  toJSON(): object | null;
}

export interface OpenAIAgentsExporterStats {
  traces: number;
  spans: number;
  bufferedSpans: number;
  cachedTraces: number;
  failedExports: number;
  lastError?: string;
}

/**
 * A structurally typed exporter for `@openai/agents`' BatchTraceProcessor.
 *
 * No runtime dependency on the agent SDK is required. OpenAI Agents exports a
 * trace at start and completed spans later, so every SDK batch becomes an
 * idempotent Farfield segment instead of waiting for a nonexistent close item.
 */
export class FarfieldOpenAIAgentsExporter {
  readonly #client: Farfield;
  readonly #defaultAgent: string | undefined;
  readonly #maxTraceCache: number;
  readonly #traces = new Map<string, Payload>();
  readonly #pending = new Map<string, Payload[]>();
  #tracesCommitted = 0;
  #spansCommitted = 0;
  #failedExports = 0;
  #lastError: string | undefined;

  constructor(client: Farfield, options: { defaultAgent?: string; maxTraceCache?: number } = {}) {
    this.#client = client;
    this.#defaultAgent = options.defaultAgent;
    this.#maxTraceCache = options.maxTraceCache ?? 8192;
    if (this.#maxTraceCache < 1) throw new TypeError("farfield: maxTraceCache must be positive");
  }

  async export(items: OpenAIAgentsTraceItem[], signal?: AbortSignal): Promise<void> {
    signal?.throwIfAborted();
    const payloads = items.map(exportPayload);
    const newTraces = new Set<string>();
    for (const payload of payloads) {
      if (payload.object !== "trace") continue;
      const traceId = stringValue(payload.id);
      if (!traceId) continue;
      this.#traces.delete(traceId);
      this.#traces.set(traceId, payload);
      while (this.#traces.size > this.#maxTraceCache) {
        const oldest = this.#traces.keys().next().value as string | undefined;
        if (oldest === undefined) break;
        this.#traces.delete(oldest);
      }
      newTraces.add(traceId);
    }
    for (const payload of payloads) {
      if (payload.object !== "trace.span") continue;
      const traceId = stringValue(payload.trace_id);
      if (!traceId) continue;
      const spans = this.#pending.get(traceId) ?? [];
      spans.push(payload);
      this.#pending.set(traceId, spans);
    }

    // The official processor exports trace envelopes before their spans. If a
    // custom processor violates that order, commit the spans under their trace
    // ID immediately instead of retaining an unbounded orphan buffer.
    const ready = new Set([...newTraces, ...this.#pending.keys()]);
    for (const traceId of ready) {
      signal?.throwIfAborted();
      const trace = this.#traces.get(traceId) ?? {
        object: "trace",
        id: traceId,
        workflow_name: "OpenAI Agent workflow",
      };
      const spans = this.#pending.get(traceId) ?? [];
      this.#pending.delete(traceId);
      await this.#commitBatch(trace, spans, newTraces.has(traceId), signal);
    }
  }

  async flush(signal?: AbortSignal): Promise<void> {
    const pending = [...this.#pending];
    this.#pending.clear();
    for (const [traceId, spans] of pending) {
      signal?.throwIfAborted();
      const known = this.#traces.get(traceId);
      const trace = known ?? {
        object: "trace",
        id: traceId,
        workflow_name: "OpenAI Agent workflow",
      };
      await this.#commitBatch(trace, spans, known === undefined, signal);
    }
  }

  shutdown(signal?: AbortSignal): Promise<void> {
    return this.flush(signal).finally(() => this.#traces.clear());
  }

  stats(): OpenAIAgentsExporterStats {
    return {
      traces: this.#tracesCommitted,
      spans: this.#spansCommitted,
      bufferedSpans: [...this.#pending.values()].reduce((total, spans) => total + spans.length, 0),
      cachedTraces: this.#traces.size,
      failedExports: this.#failedExports,
      ...(this.#lastError === undefined ? {} : { lastError: this.#lastError }),
    };
  }

  async #commitBatch(
    trace: Payload,
    spans: Payload[],
    includeTrace: boolean,
    signal?: AbortSignal,
  ): Promise<void> {
    const traceId = stringValue(trace.id) ?? "unknown";
    const conversationId = externalId(stringValue(trace.group_id) ?? traceId, "conv_openai_");
    const workflow = stringValue(trace.workflow_name) ?? "OpenAI Agent workflow";
    const events: Event[] = [];
    if (includeTrace) {
      events.push({
        id: recordId("openai_trace_", traceId),
        conversationId,
        kind: "agent.trace",
        content: { schema: "farfield.openai_agents.trace.v1", trace: jsonValue(trace) },
        traceId,
        ...(this.#defaultAgent === undefined ? {} : { agent: this.#defaultAgent }),
        tags: { "farfield.source": "openai-agents", workflow: workflow.slice(0, 1024) },
      });
    }
    events.push(...spans.map((span) => this.#spanEvent(conversationId, workflow, span)));
    if (events.length === 0) return;

    try {
      const prepared = await Promise.all(events.map((event) => this.#client.prepareEvent(event)));
      const seed = prepared.map((event) => event.id).sort().join("\n");
      const segmentId = `openai_${sha256(seed)}`;
      await this.#client.capturePreparedBatch(prepared, { segmentId, ...(signal ? { signal } : {}) });
      this.#tracesCommitted += includeTrace ? 1 : 0;
      this.#spansCommitted += spans.length;
    } catch (error) {
      this.#failedExports += 1;
      this.#lastError = error instanceof Error ? error.message : String(error);
      throw error;
    }
  }

  #spanEvent(conversationId: string, workflow: string, span: Payload): Event {
    const data = payloadValue(span.span_data);
    const rawType = stringValue(data.type) ?? "custom";
    const custom = payloadValue(data.data);
    const spanType = rawType === "custom" ? stringValue(custom.sdk_span_type) ?? rawType : rawType;
    const traceId = stringValue(span.trace_id);
    const spanId = stringValue(span.id);
    const parentId = stringValue(span.parent_id);
    const agent =
      spanType === "agent"
        ? stringValue(data.name) ?? this.#defaultAgent
        : spanType === "turn"
          ? stringValue(custom.agent_name) ?? this.#defaultAgent
          : this.#defaultAgent;
    const tool =
      spanType === "function"
        ? stringValue(data.name)
        : spanType === "mcp_tools"
          ? stringValue(data.server)
          : undefined;
    return {
      id: recordId("openai_span_", spanId ?? sha256(JSON.stringify(span))),
      conversationId,
      kind: spanKind(spanType),
      content: { schema: "farfield.openai_agents.span.v1", span: jsonValue(span) },
      ...(stringValue(span.started_at) ? { occurredAt: stringValue(span.started_at)! } : {}),
      ...(traceId ? { traceId } : {}),
      ...(spanId ? { spanId } : {}),
      ...(parentId ? { parentId } : {}),
      ...(agent ? { agent } : {}),
      ...(tool ? { tool } : {}),
      status: span.error ? "error" : "ok",
      tags: {
        "farfield.source": "openai-agents",
        "openai.span.type": spanType.slice(0, 1024),
        workflow: workflow.slice(0, 1024),
      },
    };
  }
}

type Payload = Record<string, unknown>;

function exportPayload(item: OpenAIAgentsTraceItem): Payload {
  const value = item.toJSON();
  if (!value || Array.isArray(value)) throw new TypeError("farfield: OpenAI Agents toJSON() must return an object");
  return value as Payload;
}

function spanKind(type: string): string {
  const known: Record<string, string> = {
    agent: "agent.invoke",
    task: "agent.task",
    turn: "agent.turn",
    generation: "model.generation",
    response: "model.generation",
    function: "tool.execution",
    mcp_tools: "tool.execution",
    handoff: "agent.handoff",
    guardrail: "guardrail",
    transcription: "voice.transcription",
    speech: "voice.synthesis",
    speech_group: "voice.session",
    custom: "trace.span",
  };
  return (known[type] ?? `openai.${type}`).slice(0, 128);
}

function payloadValue(value: unknown): Payload {
  return value && typeof value === "object" && !Array.isArray(value) ? (value as Payload) : {};
}

function stringValue(value: unknown): string | undefined {
  return typeof value === "string" && value.length > 0 ? value : undefined;
}

function recordId(prefix: string, value: string): string {
  const candidate = prefix + value;
  return candidate.length <= 255 && validId(candidate) ? candidate : prefix + sha256(value);
}

function externalId(value: string, prefix: string): string {
  return validId(value) ? value : prefix + sha256(value);
}

function validId(value: string): boolean {
  return value.length > 0 && value.length <= 255 && /^[A-Za-z0-9][A-Za-z0-9._:@/-]*$/.test(value);
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
