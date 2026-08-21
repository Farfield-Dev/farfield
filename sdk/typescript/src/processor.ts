import { DroppedEvent } from "./errors.js";
import { Farfield } from "./client.js";
import type { Event, WireEvent } from "./types.js";

export interface BackgroundProcessorOptions {
  maxQueueSize?: number;
  maxBatchSize?: number;
  scheduleDelayMs?: number;
  overflow?: "drop" | "throw";
  onError?: (error: unknown) => void | Promise<void>;
}

export interface ProcessorStats {
  enqueued: number;
  committed: number;
  dropped: number;
  failed: number;
  batches: number;
  pending: number;
  lastError?: string;
}

/**
 * Bounded, opt-in background batching for high-volume capture.
 *
 * submit() acknowledges queue admission, not durable storage. await flush()
 * when every admitted event must have reached Farfield. Direct client writes
 * retain their durable-acknowledgment semantics.
 */
export class BackgroundProcessor {
  readonly client: Farfield;
  readonly maxQueueSize: number;
  readonly maxBatchSize: number;
  readonly scheduleDelayMs: number;
  readonly overflow: "drop" | "throw";
  readonly onError: BackgroundProcessorOptions["onError"];

  readonly #queue: WireEvent[] = [];
  #timer: ReturnType<typeof setTimeout> | undefined;
  #draining: Promise<void> | undefined;
  #closed = false;
  #stats: ProcessorStats = { enqueued: 0, committed: 0, dropped: 0, failed: 0, batches: 0, pending: 0 };

  constructor(client: Farfield, options: BackgroundProcessorOptions = {}) {
    this.client = client;
    this.maxQueueSize = options.maxQueueSize ?? 8192;
    this.maxBatchSize = options.maxBatchSize ?? 128;
    this.scheduleDelayMs = options.scheduleDelayMs ?? 250;
    this.overflow = options.overflow ?? "drop";
    this.onError = options.onError;
    if (this.maxQueueSize < 1 || this.maxBatchSize < 1 || this.scheduleDelayMs < 0) {
      throw new TypeError("farfield: processor queue and batch sizes must be positive");
    }
  }

  /** Snapshot caller context and queue an event; true means admitted. */
  async submit(event: Event): Promise<boolean> {
    if (this.#closed) throw new Error("farfield: processor is shut down");
    let prepared: WireEvent;
    try {
      prepared = await this.client.prepareEvent(event);
    } catch (error) {
      if (!(error instanceof DroppedEvent)) throw error;
      this.#stats.dropped += 1;
      return false;
    }
    if (this.#closed) throw new Error("farfield: processor is shut down");
    if (this.#queue.length >= this.maxQueueSize) {
      this.#stats.dropped += 1;
      if (this.overflow === "throw") throw new Error("farfield: background capture queue is full");
      return false;
    }
    this.#queue.push(prepared);
    this.#stats.enqueued += 1;
    this.#stats.pending += 1;
    if (this.#queue.length >= this.maxBatchSize) {
      this.#clearTimer();
      void this.#drain();
    } else {
      this.#schedule();
    }
    return true;
  }

  /** Deliver all currently admitted events; false means at least one failed. */
  async flush(): Promise<boolean> {
    this.#clearTimer();
    while (this.#queue.length > 0 || this.#draining) await this.#drain();
    return this.#stats.failed === 0;
  }

  async shutdown(): Promise<boolean> {
    if (this.#closed) return this.#queue.length === 0 && !this.#draining && this.#stats.failed === 0;
    this.#closed = true;
    return this.flush();
  }

  stats(): Readonly<ProcessorStats> {
    return { ...this.#stats };
  }

  #schedule(): void {
    if (this.#timer || this.#draining) return;
    this.#timer = setTimeout(() => {
      this.#timer = undefined;
      void this.#drain();
    }, this.scheduleDelayMs);
  }

  #clearTimer(): void {
    if (this.#timer) clearTimeout(this.#timer);
    this.#timer = undefined;
  }

  async #drain(): Promise<void> {
    if (this.#draining) return this.#draining;
    if (this.#queue.length === 0) return;
    this.#draining = this.#deliverNext();
    try {
      await this.#draining;
    } finally {
      this.#draining = undefined;
      if (this.#queue.length > 0 && !this.#closed) this.#schedule();
    }
  }

  async #deliverNext(): Promise<void> {
    const events = this.#queue.splice(0, this.maxBatchSize);
    const groups = new Map<string, WireEvent[]>();
    for (const event of events) {
      const values = groups.get(event.conversation_id) ?? [];
      values.push(event);
      groups.set(event.conversation_id, values);
    }
    for (const values of groups.values()) {
      try {
        await this.client.capturePreparedBatch(values);
        this.#stats.committed += values.length;
        this.#stats.batches += 1;
      } catch (error) {
        this.#stats.failed += values.length;
        this.#stats.lastError = errorMessage(error);
        try {
          await this.onError?.(error);
        } catch {
          // Error observers must not break the processor.
        }
      } finally {
        this.#stats.pending -= values.length;
      }
    }
  }
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
