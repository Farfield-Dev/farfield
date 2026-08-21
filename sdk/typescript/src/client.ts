import { AsyncLocalStorage } from "node:async_hooks";
import { randomBytes } from "node:crypto";

import { APIError, DroppedEvent, TransportError } from "./errors.js";
import type {
  ConversationSummary,
  Entry,
  Event,
  HistoryRecord,
  Json,
  Query,
  RequestOptions,
  Run,
  RunStatus,
  RuntimeEvent,
  SearchQuery,
  SearchResult,
  Scope,
  Segment,
  WireEvent,
} from "./types.js";

export const VERSION = "0.1.0-alpha.1";
const DEFAULT_ENDPOINT = "http://127.0.0.1:8787";
const RETRYABLE = new Set([408, 425, 429, 500, 502, 503, 504]);

export interface FarfieldOptions {
  endpoint?: string;
  token?: string;
  timeoutMs?: number;
  maxRetries?: number;
  retryDelayMs?: number;
  headers?: Record<string, string>;
  defaults?: Scope;
  beforeSend?: (event: Readonly<WireEvent>) => WireEvent | null | Promise<WireEvent | null>;
  fetch?: typeof globalThis.fetch;
}

export interface ConversationOptions extends Omit<Scope, "conversationId"> {
  id?: string;
}

export interface CreateRunInput {
  id?: string;
  operationId?: string;
  checkpoint?: Json;
}

export interface TransitionRunInput {
  operationId?: string;
  checkpoint?: Json;
}

export interface CheckpointRunInput {
  operationId?: string;
  checkpoint: Json;
}

export class Farfield {
  readonly endpoint: string;
  readonly token: string | undefined;
  readonly timeoutMs: number;
  readonly maxRetries: number;
  readonly retryDelayMs: number;
  readonly headers: Record<string, string>;
  readonly defaults: Scope;

  readonly #beforeSend: FarfieldOptions["beforeSend"] | undefined;
  readonly #fetch: typeof globalThis.fetch;
  readonly #scope = new AsyncLocalStorage<Scope>();

  constructor(options: FarfieldOptions = {}) {
    this.endpoint = (options.endpoint ?? process.env.FARFIELD_ENDPOINT ?? DEFAULT_ENDPOINT).replace(/\/+$/, "");
    const endpoint = new URL(this.endpoint);
    if (!(["http:", "https:"] as string[]).includes(endpoint.protocol) || endpoint.search || endpoint.hash) {
      throw new TypeError(`farfield: invalid endpoint ${JSON.stringify(this.endpoint)}`);
    }
    this.token = options.token ?? process.env.FARFIELD_TOKEN;
    this.timeoutMs = options.timeoutMs ?? 10_000;
    this.maxRetries = options.maxRetries ?? 2;
    this.retryDelayMs = options.retryDelayMs ?? 100;
    if (this.timeoutMs <= 0 || this.maxRetries < 0 || this.retryDelayMs < 0) {
      throw new TypeError("farfield: timeout must be positive; retries cannot be negative");
    }
    this.headers = { ...options.headers };
    this.defaults = cloneScope(options.defaults ?? {});
    this.#beforeSend = options.beforeSend;
    this.#fetch = options.fetch ?? globalThis.fetch;
  }

  async capture(event: Event, options: RequestOptions = {}): Promise<HistoryRecord> {
    const prepared = await this.#prepare(event);
    return this.#request("POST", "/v1/history/records", prepared, options);
  }

  async captureBatch(
    events: readonly Event[],
    options: RequestOptions & { segmentId?: string } = {},
  ): Promise<Segment> {
    if (events.length === 0) throw new TypeError("farfield: batch requires at least one event");
    const prepared: WireEvent[] = [];
    let conversationId: string | undefined;
    for (const event of events) {
      try {
        const value = await this.#prepare(event);
        conversationId ??= value.conversation_id;
        if (value.conversation_id !== conversationId) {
          throw new TypeError("farfield: every event in a batch must belong to one conversation");
        }
        prepared.push(value);
      } catch (error) {
        if (!(error instanceof DroppedEvent)) throw error;
      }
    }
    if (prepared.length === 0) throw new DroppedEvent("farfield: every event in the batch was dropped");
    return this.#request(
      "POST",
      "/v1/history/segments",
      { id: options.segmentId ?? id("seg_"), records: prepared },
      options,
    );
  }

  async withConversation<T>(
    options: ConversationOptions | string,
    callback: (conversation: Conversation) => Promise<T> | T,
  ): Promise<T> {
    const value = typeof options === "string" ? { id: options } : options;
    const scope = mergeScope(this.#activeScope(), compact({
      conversationId: value.id ?? id("conv_"),
      traceId: value.traceId,
      spanId: value.spanId,
      parentId: value.parentId,
      agent: value.agent,
      tags: value.tags,
    }) as Scope);
    return this.#scope.run(scope, () => callback(new Conversation(this, scope.conversationId!)));
  }

  conversation(conversationId: string): Conversation {
    return new Conversation(this, conversationId);
  }

  async batch<T>(
    options: { conversationId?: string; segmentId?: string },
    callback: (batch: Batch) => Promise<T> | T,
  ): Promise<Segment> {
    const conversationId = options.conversationId ?? this.#activeScope().conversationId ?? id("conv_");
    const batch = new Batch(conversationId);
    await callback(batch);
    return this.captureBatch(batch.events, options.segmentId ? { segmentId: options.segmentId } : {});
  }

  async query(query: Query = {}, options: RequestOptions = {}): Promise<HistoryRecord[]> {
    const limit = query.limit ?? 100;
    validateLimit(limit);
    const parameters = new URLSearchParams({ limit: String(limit) });
    add(parameters, "conversation_id", query.conversationId);
    add(parameters, "trace_id", query.traceId);
    add(parameters, "kind", query.kind);
    add(parameters, "agent", query.agent);
    add(parameters, "tool", query.tool);
    add(parameters, "status", query.status);
    add(parameters, "since", query.since instanceof Date ? query.since.toISOString() : query.since);
    add(parameters, "until", query.until instanceof Date ? query.until.toISOString() : query.until);
    for (const key of Object.keys(query.tags ?? {}).sort()) parameters.append("tag", `${key}=${query.tags![key]}`);
    return this.#request("GET", `/v1/history/records?${parameters}`, undefined, options);
  }

  async search(query: SearchQuery = {}, options: RequestOptions = {}): Promise<SearchResult> {
    const limit = query.limit ?? 100;
    validateLimit(limit);
    const parameters = new URLSearchParams({ limit: String(limit) });
    add(parameters, "q", query.text);
    add(parameters, "conversation_id", query.conversationId);
    add(parameters, "trace_id", query.traceId);
    add(parameters, "kind", query.kind);
    add(parameters, "agent", query.agent);
    add(parameters, "tool", query.tool);
    add(parameters, "status", query.status);
    add(parameters, "since", query.since instanceof Date ? query.since.toISOString() : query.since);
    add(parameters, "until", query.until instanceof Date ? query.until.toISOString() : query.until);
    for (const key of Object.keys(query.tags ?? {}).sort()) parameters.append("tag", `${key}=${query.tags![key]}`);
    return this.#request("GET", `/v1/history/search?${parameters}`, undefined, options);
  }

  getRecord(recordId: string, options: RequestOptions = {}): Promise<Entry> {
    return this.#request("GET", `/v1/history/records/${encodeURIComponent(recordId)}`, undefined, options);
  }

  conversations(limit = 100, options: RequestOptions = {}): Promise<ConversationSummary[]> {
    validateLimit(limit);
    return this.#request("GET", `/v1/history/conversations?limit=${limit}`, undefined, options);
  }

  timeline(conversationId: string, options: RequestOptions = {}): Promise<Entry[]> {
    return this.#request(
      "GET",
      `/v1/history/conversations/${encodeURIComponent(conversationId)}/timeline`,
      undefined,
      options,
    );
  }

  async health(options: RequestOptions = {}): Promise<boolean> {
    const value = await this.#request<{ ok: boolean; service: string }>("GET", "/v1/health", undefined, options);
    return value.ok && value.service === "farfield";
  }

  createRun(input: CreateRunInput = {}, options: RequestOptions = {}): Promise<RuntimeEvent> {
    return this.#request(
      "POST",
      "/v1/runtime/runs",
      compact({ id: input.id ?? id("run_"), operation_id: input.operationId ?? id("op_"), checkpoint: input.checkpoint }),
      options,
    );
  }

  getRun(runId: string, options: RequestOptions = {}): Promise<Run> {
    return this.#request("GET", `/v1/runtime/runs/${encodeURIComponent(runId)}`, undefined, options);
  }

  runEvents(runId: string, options: RequestOptions = {}): Promise<RuntimeEvent[]> {
    return this.#request("GET", `/v1/runtime/runs/${encodeURIComponent(runId)}/events`, undefined, options);
  }

  transitionRun(
    runId: string,
    to: RunStatus,
    input: TransitionRunInput = {},
    options: RequestOptions = {},
  ): Promise<RuntimeEvent> {
    return this.#request(
      "POST",
      `/v1/runtime/runs/${encodeURIComponent(runId)}/transitions`,
      compact({ operation_id: input.operationId ?? id("op_"), to, checkpoint: input.checkpoint }),
      options,
    );
  }

  checkpointRun(runId: string, input: CheckpointRunInput, options: RequestOptions = {}): Promise<RuntimeEvent> {
    return this.#request(
      "POST",
      `/v1/runtime/runs/${encodeURIComponent(runId)}/checkpoints`,
      { operation_id: input.operationId ?? id("op_"), checkpoint: input.checkpoint },
      options,
    );
  }

  async #prepare(event: Event): Promise<WireEvent> {
    const scope = this.#activeScope();
    let prepared: WireEvent = compact({
      id: event.id ?? id("rec_"),
      conversation_id: event.conversationId ?? scope.conversationId,
      kind: event.kind,
      content: event.content,
      occurred_at: occurredAt(event.occurredAt),
      sequence: event.sequence,
      trace_id: event.traceId ?? scope.traceId,
      span_id: event.spanId ?? scope.spanId,
      parent_id: event.parentId ?? scope.parentId,
      agent: event.agent ?? scope.agent,
      tool: event.tool,
      status: event.status,
      tags: { ...(scope.tags ?? {}), ...(event.tags ?? {}) },
    }) as WireEvent;
    if (this.#beforeSend) {
      const transformed = await this.#beforeSend(structuredClone(prepared));
      if (transformed === null) throw new DroppedEvent();
      prepared = transformed;
    }
    prepared = {
      ...prepared,
      id: prepared.id || id("rec_"),
      occurred_at: prepared.occurred_at || new Date().toISOString(),
      tags: prepared.tags ?? {},
    };
    if (!prepared.conversation_id || !prepared.kind) {
      throw new TypeError("farfield: conversationId and kind are required");
    }
    encode(prepared);
    return prepared;
  }

  #activeScope(): Scope {
    return mergeScope(this.defaults, this.#scope.getStore());
  }

  async #request<T>(
    method: string,
    path: string,
    payload?: unknown,
    options: RequestOptions = {},
  ): Promise<T> {
    const body = payload === undefined ? undefined : encode(payload);
    for (let attempt = 0; attempt <= this.maxRetries; attempt += 1) {
      const controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(new Error("request timed out")), this.timeoutMs);
      const abort = () => controller.abort(options.signal?.reason);
      options.signal?.addEventListener("abort", abort, { once: true });
      if (options.signal?.aborted) abort();
      try {
        const requestHeaders: Record<string, string> = {
          Accept: "application/json",
          "User-Agent": `farfield-typescript/${VERSION}`,
          ...this.headers,
        };
        if (body !== undefined) requestHeaders["Content-Type"] = "application/json";
        if (this.token) requestHeaders.Authorization = `Bearer ${this.token}`;
        const requestInit: RequestInit = {
          method,
          signal: controller.signal,
          headers: requestHeaders,
        };
        if (body !== undefined) requestInit.body = body;
        const response = await this.#fetch(this.endpoint + path, requestInit);
        const text = await response.text();
        if (response.ok) return decode<T>(text, method, path);
        const retryable = RETRYABLE.has(response.status);
        if (retryable && attempt < this.maxRetries && !options.signal?.aborted) {
          await wait(retryDelay(response.headers.get("Retry-After"), this.retryDelayMs * 2 ** attempt), options.signal);
          continue;
        }
        throw apiError(response.status, text, retryable);
      } catch (error) {
        if (error instanceof APIError || error instanceof TransportError) throw error;
        if (attempt < this.maxRetries && !options.signal?.aborted) {
          await wait(this.retryDelayMs * 2 ** attempt, options.signal);
          continue;
        }
        throw new TransportError(`farfield: ${method} ${path}: ${message(error)}`, error);
      } finally {
        clearTimeout(timeout);
        options.signal?.removeEventListener("abort", abort);
      }
    }
    throw new TransportError("farfield: retry budget exhausted");
  }
}

export class Conversation {
  constructor(
    private readonly client: Farfield,
    readonly id: string,
  ) {}

  capture(event: Omit<Event, "conversationId">, options?: RequestOptions): Promise<HistoryRecord> {
    return this.client.capture({ ...event, conversationId: this.id }, options);
  }

  message(role: string, content: Json, event: Partial<Omit<Event, "kind" | "content" | "conversationId">> = {}) {
    return this.capture({ ...event, kind: `message.${role}`, content });
  }

  toolResult(tool: string, content: Json, event: Partial<Omit<Event, "kind" | "content" | "conversationId">> = {}) {
    return this.capture({ status: "completed", ...event, kind: "tool.result", tool, content });
  }

  batch<T>(callback: (batch: Batch) => Promise<T> | T, segmentId?: string): Promise<Segment> {
    return this.client.batch(segmentId ? { conversationId: this.id, segmentId } : { conversationId: this.id }, callback);
  }
}

export class Batch {
  readonly events: Event[] = [];

  constructor(readonly conversationId: string) {}

  capture(event: Omit<Event, "conversationId">): Event {
    const value = { ...event, conversationId: this.conversationId };
    this.events.push(value);
    return value;
  }

  message(role: string, content: Json, event: Partial<Omit<Event, "kind" | "content" | "conversationId">> = {}) {
    return this.capture({ ...event, kind: `message.${role}`, content });
  }

  toolResult(tool: string, content: Json, event: Partial<Omit<Event, "kind" | "content" | "conversationId">> = {}) {
    return this.capture({ status: "completed", ...event, kind: "tool.result", tool, content });
  }
}

function id(prefix: string): string {
  return prefix + randomBytes(16).toString("hex");
}

function occurredAt(value?: Date | string): string {
  if (value instanceof Date) {
    if (Number.isNaN(value.valueOf())) throw new TypeError("farfield: occurredAt is not a valid date");
    return value.toISOString();
  }
  if (value !== undefined) {
    const parsed = new Date(value);
    if (Number.isNaN(parsed.valueOf())) throw new TypeError("farfield: occurredAt is not a valid timestamp");
    return parsed.toISOString();
  }
  return new Date().toISOString();
}

function cloneScope(value: Scope): Scope {
  return { ...value, tags: { ...(value.tags ?? {}) } };
}

function mergeScope(base?: Scope, overlay?: Scope): Scope {
  const result = compact({ ...base, ...overlay, tags: { ...(base?.tags ?? {}), ...(overlay?.tags ?? {}) } });
  return result as Scope;
}

function compact<T extends Record<string, unknown>>(value: T): T {
  return Object.fromEntries(Object.entries(value).filter(([, item]) => item !== undefined)) as T;
}

function encode(value: unknown): string {
  try {
    validateJson(value, "$", new Set<object>());
    const body = JSON.stringify(value);
    if (body === undefined) throw new TypeError("value is undefined");
    return body;
  } catch (error) {
    throw new TypeError(`farfield: value is not valid JSON: ${message(error)}`);
  }
}

function validateJson(value: unknown, path: string, ancestors: Set<object>): void {
  if (value === null || typeof value === "string" || typeof value === "boolean") return;
  if (typeof value === "number") {
    if (!Number.isFinite(value)) throw new TypeError(`${path} contains a non-finite number`);
    return;
  }
  if (typeof value !== "object") throw new TypeError(`${path} contains ${typeof value}`);
  if (ancestors.has(value)) throw new TypeError(`${path} contains a circular reference`);
  ancestors.add(value);
  try {
    if (Array.isArray(value)) {
      for (let index = 0; index < value.length; index += 1) {
        if (!(index in value)) throw new TypeError(`${path}[${index}] is a sparse array element`);
        validateJson(value[index], `${path}[${index}]`, ancestors);
      }
      return;
    }
    const prototype: object | null = Object.getPrototypeOf(value) as object | null;
    if (prototype !== Object.prototype && prototype !== null) {
      throw new TypeError(`${path} is not a plain JSON object`);
    }
    for (const [key, item] of Object.entries(value)) validateJson(item, `${path}.${key}`, ancestors);
  } finally {
    ancestors.delete(value);
  }
}

function decode<T>(value: string, method: string, path: string): T {
  if (!value) return undefined as T;
  try {
    return JSON.parse(value) as T;
  } catch (error) {
    throw new TransportError(`farfield: ${method} ${path}: response was not valid JSON`, error);
  }
}

function apiError(statusCode: number, body: string, retryable: boolean): APIError {
  try {
    const value = JSON.parse(body) as { error?: { code?: string; message?: string; retryable?: boolean } };
    if (value.error?.message) {
      return new APIError(statusCode, value.error.code ?? "", value.error.message, retryable || value.error.retryable === true);
    }
  } catch {}
  return new APIError(statusCode, "", body.trim() || `HTTP ${statusCode}`, retryable);
}

function retryDelay(value: string | null, fallback: number): number {
  if (!value) return fallback;
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds >= 0) return seconds * 1_000;
  const date = Date.parse(value);
  return Number.isNaN(date) ? fallback : Math.max(0, date - Date.now());
}

function wait(milliseconds: number, signal?: AbortSignal): Promise<void> {
  if (milliseconds <= 0) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const cleanup = () => signal?.removeEventListener("abort", abort);
    const timer = setTimeout(() => {
      cleanup();
      resolve();
    }, milliseconds);
    const abort = () => {
      clearTimeout(timer);
      cleanup();
      reject(signal?.reason ?? new Error("request aborted"));
    };
    if (signal?.aborted) abort();
    else signal?.addEventListener("abort", abort, { once: true });
  });
}

function validateLimit(limit: number): void {
  if (!Number.isInteger(limit) || limit < 1 || limit > 1_000) {
    throw new TypeError("farfield: limit must be an integer between 1 and 1000");
  }
}

function add(parameters: URLSearchParams, key: string, value: string | undefined): void {
  if (value !== undefined) parameters.set(key, value);
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
