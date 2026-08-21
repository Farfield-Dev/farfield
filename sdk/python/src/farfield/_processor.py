from __future__ import annotations

import queue
import threading
import time
from collections.abc import Callable
from contextlib import suppress
from dataclasses import dataclass
from typing import Literal

from ._client import Farfield
from ._errors import DroppedEvent
from ._models import Event

OverflowPolicy = Literal["drop", "raise", "block"]


@dataclass(frozen=True, slots=True)
class ProcessorStats:
    enqueued: int
    committed: int
    dropped: int
    failed: int
    batches: int
    pending: int
    last_error: str | None


class BackgroundProcessor:
    """Bounded, opt-in background batching for high-volume capture.

    ``submit`` acknowledges queue admission, not durable storage. Call ``flush``
    when every admitted event must have reached Farfield. Direct client writes
    retain their durable-acknowledgment semantics.
    """

    def __init__(
        self,
        client: Farfield,
        *,
        max_queue_size: int = 8192,
        max_batch_size: int = 128,
        schedule_delay: float = 0.25,
        overflow: OverflowPolicy = "drop",
        on_error: Callable[[Exception], None] | None = None,
    ) -> None:
        if max_queue_size < 1 or max_batch_size < 1 or schedule_delay < 0:
            raise ValueError("farfield: processor queue and batch sizes must be positive")
        if overflow not in {"drop", "raise", "block"}:
            raise ValueError("farfield: overflow must be drop, raise, or block")
        self.client = client
        self.max_batch_size = max_batch_size
        self.schedule_delay = schedule_delay
        self.overflow = overflow
        self.on_error = on_error
        self._queue: queue.Queue[Event | object] = queue.Queue(max_queue_size)
        self._stop = object()
        self._condition = threading.Condition()
        self._closed = False
        self._enqueued = 0
        self._committed = 0
        self._dropped = 0
        self._failed = 0
        self._batches = 0
        self._pending = 0
        self._last_error: str | None = None
        self._thread = threading.Thread(target=self._run, name="farfield-capture", daemon=True)
        self._thread.start()

    def __enter__(self) -> BackgroundProcessor:
        return self

    def __exit__(self, exc_type: object, exc: object, traceback: object) -> None:
        self.shutdown()

    def submit(self, event: Event, *, timeout: float | None = None) -> bool:
        """Snapshot caller context and queue an event; ``True`` means admitted."""
        try:
            prepared = self.client.prepare_event(event)
        except DroppedEvent:
            with self._condition:
                self._dropped += 1
            return False
        with self._condition:
            if self._closed:
                raise RuntimeError("farfield: processor is shut down")
            try:
                if self.overflow == "block":
                    self._queue.put(prepared, timeout=timeout)
                else:
                    self._queue.put_nowait(prepared)
            except queue.Full:
                self._dropped += 1
                if self.overflow == "raise":
                    raise BufferError("farfield: background capture queue is full") from None
                return False
            self._enqueued += 1
            self._pending += 1
        return True

    def flush(self, timeout: float | None = None) -> bool:
        """Wait for events admitted before this call; return false on failure/timeout."""
        deadline = None if timeout is None else time.monotonic() + timeout
        with self._condition:
            target = self._enqueued
            while self._committed + self._failed < target:
                remaining = None if deadline is None else deadline - time.monotonic()
                if remaining is not None and remaining <= 0:
                    return False
                self._condition.wait(remaining)
            return self._failed == 0

    def shutdown(self, *, timeout: float | None = 10.0, flush: bool = True) -> bool:
        with self._condition:
            if self._closed:
                return not self._thread.is_alive() and self._failed == 0
            self._closed = True
        delivered = self.flush(timeout) if flush else True
        try:
            self._queue.put(self._stop, timeout=timeout)
        except queue.Full:
            return False
        self._thread.join(timeout)
        return delivered and not self._thread.is_alive()

    def stats(self) -> ProcessorStats:
        with self._condition:
            return ProcessorStats(
                enqueued=self._enqueued,
                committed=self._committed,
                dropped=self._dropped,
                failed=self._failed,
                batches=self._batches,
                pending=self._pending,
                last_error=self._last_error,
            )

    def _run(self) -> None:
        while True:
            first = self._queue.get()
            if first is self._stop:
                self._queue.task_done()
                return
            batch = [first]
            deadline = time.monotonic() + self.schedule_delay
            while len(batch) < self.max_batch_size:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    break
                try:
                    item = self._queue.get(timeout=remaining)
                except queue.Empty:
                    break
                if item is self._stop:
                    self._queue.task_done()
                    self._deliver(batch)
                    return
                batch.append(item)
            self._deliver(batch)

    def _deliver(self, batch: list[object]) -> None:
        events = [event for event in batch if isinstance(event, Event)]
        groups: dict[str, list[Event]] = {}
        for event in events:
            assert event.conversation_id is not None
            groups.setdefault(event.conversation_id, []).append(event)
        committed = 0
        failed = 0
        for values in groups.values():
            try:
                self.client.capture_prepared_batch(values)
                committed += len(values)
                with self._condition:
                    self._batches += 1
            except Exception as error:
                failed += len(values)
                with self._condition:
                    self._last_error = str(error)
                if self.on_error is not None:
                    with suppress(Exception):
                        self.on_error(error)
        for _ in batch:
            self._queue.task_done()
        with self._condition:
            self._committed += committed
            self._failed += failed
            self._pending -= len(events)
            self._condition.notify_all()
