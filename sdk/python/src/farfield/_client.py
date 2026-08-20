from __future__ import annotations

import asyncio
import json
import os
import secrets
import time
from collections.abc import Callable, Iterable, Mapping
from contextlib import AbstractAsyncContextManager, AbstractContextManager
from dataclasses import replace
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
from types import TracebackType
from typing import Any, cast
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlencode, urlparse
from urllib.request import Request, urlopen

from ._context import get_scope, reset_scope, set_scope
from ._errors import APIError, DroppedEvent, TransportError
from ._models import (
    JSON,
    ConversationSummary,
    Entry,
    Event,
    Record,
    Run,
    RuntimeEvent,
    Scope,
    Segment,
)

VERSION = "0.1.0-alpha.1"
DEFAULT_ENDPOINT = "http://127.0.0.1:8787"
RETRYABLE_STATUS = {408, 425, 429, 500, 502, 503, 504}

BeforeSend = Callable[[Event], Event | None]


class Farfield:
    """Durable-by-default Farfield History and Runtime client."""

    def __init__(
        self,
        *,
        endpoint: str | None = None,
        token: str | None = None,
        timeout: float = 10.0,
        max_retries: int = 2,
        retry_delay: float = 0.1,
        headers: Mapping[str, str] | None = None,
        defaults: Scope | None = None,
        before_send: BeforeSend | None = None,
    ) -> None:
        self.endpoint = (endpoint or os.getenv("FARFIELD_ENDPOINT") or DEFAULT_ENDPOINT).rstrip("/")
        parsed = urlparse(self.endpoint)
        if (
            parsed.scheme not in {"http", "https"}
            or not parsed.netloc
            or parsed.query
            or parsed.fragment
        ):
            raise ValueError(f"farfield: invalid endpoint {self.endpoint!r}")
        if timeout <= 0 or max_retries < 0 or retry_delay < 0:
            raise ValueError("farfield: timeout must be positive; retries cannot be negative")
        self.token = token if token is not None else os.getenv("FARFIELD_TOKEN")
        self.timeout = timeout
        self.max_retries = max_retries
        self.retry_delay = retry_delay
        self.headers = dict(headers or {})
        self.defaults = defaults or Scope()
        self.before_send = before_send

    def __enter__(self) -> Farfield:
        return self

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc: BaseException | None,
        traceback: TracebackType | None,
    ) -> None:
        return None

    def capture(
        self,
        kind: str,
        content: JSON,
        *,
        conversation_id: str | None = None,
        id: str | None = None,
        occurred_at: datetime | None = None,
        sequence: int | None = None,
        trace_id: str | None = None,
        span_id: str | None = None,
        parent_id: str | None = None,
        agent: str | None = None,
        tool: str | None = None,
        status: str | None = None,
        tags: Mapping[str, str] | None = None,
    ) -> Record:
        return self.capture_event(
            Event(
                id=id,
                conversation_id=conversation_id,
                kind=kind,
                content=content,
                occurred_at=occurred_at,
                sequence=sequence,
                trace_id=trace_id,
                span_id=span_id,
                parent_id=parent_id,
                agent=agent,
                tool=tool,
                status=status,
                tags=dict(tags or {}),
            )
        )

    def capture_event(self, event: Event) -> Record:
        prepared = self._prepare_event(event)
        value = self._request("POST", "/v1/history/records", prepared.to_payload())
        return Record.from_payload(_mapping(value))

    def capture_batch(self, events: Iterable[Event], *, segment_id: str | None = None) -> Segment:
        prepared: list[Event] = []
        conversation_id: str | None = None
        for event in events:
            try:
                value = self._prepare_event(event)
            except DroppedEvent:
                continue
            if conversation_id is None:
                conversation_id = value.conversation_id
            elif value.conversation_id != conversation_id:
                raise ValueError("farfield: every event in a batch must belong to one conversation")
            prepared.append(value)
        if not prepared:
            raise DroppedEvent("farfield: every event in the batch was dropped")
        payload = {
            "id": segment_id or _id("seg_"),
            "records": [event.to_payload() for event in prepared],
        }
        return Segment.from_payload(
            _mapping(self._request("POST", "/v1/history/segments", payload))
        )

    def query(
        self,
        *,
        conversation_id: str | None = None,
        trace_id: str | None = None,
        kind: str | None = None,
        agent: str | None = None,
        tool: str | None = None,
        status: str | None = None,
        since: datetime | None = None,
        limit: int = 100,
    ) -> tuple[Record, ...]:
        if not 1 <= limit <= 1000:
            raise ValueError("farfield: limit must be between 1 and 1000")
        parameters: dict[str, str | int] = {
            key: value
            for key, value in {
                "conversation_id": conversation_id,
                "trace_id": trace_id,
                "kind": kind,
                "agent": agent,
                "tool": tool,
                "status": status,
            }.items()
            if value is not None
        }
        if since is not None:
            timestamp = Event(kind="query", content=None, occurred_at=since).to_payload()[
                "occurred_at"
            ]
            parameters["since"] = str(timestamp)
        parameters["limit"] = limit
        values = _list(self._request("GET", "/v1/history/records?" + urlencode(parameters)))
        return tuple(Record.from_payload(_mapping(value)) for value in values)

    def get_record(self, record_id: str) -> Entry:
        path = f"/v1/history/records/{quote(record_id, safe='')}"
        return Entry.from_payload(_mapping(self._request("GET", path)))

    def conversations(self, *, limit: int = 100) -> tuple[ConversationSummary, ...]:
        if not 1 <= limit <= 1000:
            raise ValueError("farfield: limit must be between 1 and 1000")
        values = _list(self._request("GET", f"/v1/history/conversations?limit={limit}"))
        return tuple(ConversationSummary.from_payload(_mapping(value)) for value in values)

    def timeline(self, conversation_id: str) -> tuple[Entry, ...]:
        path = f"/v1/history/conversations/{quote(conversation_id, safe='')}/timeline"
        values = _list(self._request("GET", path))
        return tuple(Entry.from_payload(_mapping(value)) for value in values)

    def health(self) -> bool:
        value = _mapping(self._request("GET", "/v1/health"))
        return value.get("ok") is True and value.get("service") == "farfield"

    def conversation(
        self,
        conversation_id: str | None = None,
        *,
        trace_id: str | None = None,
        agent: str | None = None,
        tags: Mapping[str, str] | None = None,
    ) -> Conversation:
        return Conversation(
            self,
            Scope(
                conversation_id=conversation_id or _id("conv_"),
                trace_id=trace_id,
                agent=agent,
                tags=dict(tags or {}),
            ),
        )

    def batch(self, conversation_id: str | None = None, *, segment_id: str | None = None) -> Batch:
        scope = get_scope()
        resolved = conversation_id or (scope.conversation_id if scope else None)
        if not resolved:
            resolved = _id("conv_")
        return Batch(self, resolved, segment_id)

    def create_run(
        self,
        *,
        run_id: str | None = None,
        operation_id: str | None = None,
        checkpoint: JSON = None,
    ) -> RuntimeEvent:
        payload: dict[str, Any] = {
            "id": run_id or _id("run_"),
            "operation_id": operation_id or _id("op_"),
        }
        if checkpoint is not None:
            payload["checkpoint"] = checkpoint
        return RuntimeEvent.from_payload(
            _mapping(self._request("POST", "/v1/runtime/runs", payload))
        )

    def get_run(self, run_id: str) -> Run:
        return Run.from_payload(
            _mapping(self._request("GET", f"/v1/runtime/runs/{quote(run_id, safe='')}"))
        )

    def run_events(self, run_id: str) -> tuple[RuntimeEvent, ...]:
        values = _list(self._request("GET", f"/v1/runtime/runs/{quote(run_id, safe='')}/events"))
        return tuple(RuntimeEvent.from_payload(_mapping(event)) for event in values)

    def transition_run(
        self,
        run_id: str,
        to: str,
        *,
        operation_id: str | None = None,
        checkpoint: JSON = None,
    ) -> RuntimeEvent:
        payload: dict[str, Any] = {"operation_id": operation_id or _id("op_"), "to": to}
        if checkpoint is not None:
            payload["checkpoint"] = checkpoint
        path = f"/v1/runtime/runs/{quote(run_id, safe='')}/transitions"
        return RuntimeEvent.from_payload(_mapping(self._request("POST", path, payload)))

    def checkpoint_run(
        self,
        run_id: str,
        checkpoint: JSON,
        *,
        operation_id: str | None = None,
    ) -> RuntimeEvent:
        payload = {"operation_id": operation_id or _id("op_"), "checkpoint": checkpoint}
        path = f"/v1/runtime/runs/{quote(run_id, safe='')}/checkpoints"
        return RuntimeEvent.from_payload(_mapping(self._request("POST", path, payload)))

    def _prepare_event(self, event: Event) -> Event:
        scope = _merge_scope(self.defaults, get_scope())
        prepared = replace(
            event,
            id=event.id or _id("rec_"),
            conversation_id=event.conversation_id or scope.conversation_id,
            occurred_at=event.occurred_at or datetime.now(timezone.utc),
            trace_id=event.trace_id or scope.trace_id,
            span_id=event.span_id or scope.span_id,
            parent_id=event.parent_id or scope.parent_id,
            agent=event.agent or scope.agent,
            tags={**scope.tags, **event.tags},
        )
        if self.before_send is not None:
            transformed = self.before_send(prepared)
            if transformed is None:
                raise DroppedEvent("farfield: event dropped by before_send")
            prepared = transformed
        prepared = replace(
            prepared,
            id=prepared.id or _id("rec_"),
            occurred_at=prepared.occurred_at or datetime.now(timezone.utc),
        )
        if not prepared.conversation_id or not prepared.kind:
            raise ValueError("farfield: conversation_id and kind are required")
        # Validate and snapshot JSON before any network attempt.
        _encode(prepared.to_payload())
        return prepared

    def _request(self, method: str, path: str, payload: Mapping[str, Any] | None = None) -> Any:
        body = _encode(payload) if payload is not None else None
        for attempt in range(self.max_retries + 1):
            headers = {
                "Accept": "application/json",
                "User-Agent": f"farfield-python/{VERSION}",
                **self.headers,
            }
            if body is not None:
                headers["Content-Type"] = "application/json"
            if self.token:
                headers["Authorization"] = f"Bearer {self.token}"
            request = Request(self.endpoint + path, data=body, headers=headers, method=method)
            try:
                with urlopen(request, timeout=self.timeout) as response:  # noqa: S310
                    return _decode(response.read())
            except HTTPError as error:
                try:
                    data = error.read()
                finally:
                    error.close()
                retryable = error.code in RETRYABLE_STATUS
                if retryable and attempt < self.max_retries:
                    self._sleep(attempt, error.headers.get("Retry-After"))
                    continue
                raise _api_error(error.code, data, retryable=retryable) from None
            except (URLError, TimeoutError, OSError) as error:
                if attempt < self.max_retries:
                    self._sleep(attempt, None)
                    continue
                raise TransportError(f"farfield: {method} {path}: {error}") from error
        raise TransportError("farfield: retry budget exhausted")

    def _sleep(self, attempt: int, retry_after: str | None) -> None:
        delay = _retry_after(retry_after)
        time.sleep(delay if delay is not None else self.retry_delay * (2**attempt))


class Conversation(AbstractContextManager["Conversation"]):
    def __init__(self, client: Farfield, scope: Scope) -> None:
        self.client = client
        self.scope = scope
        self._token: Any = None

    @property
    def id(self) -> str:
        assert self.scope.conversation_id is not None
        return self.scope.conversation_id

    def __enter__(self) -> Conversation:
        self._token = set_scope(_merge_scope(get_scope(), self.scope))
        return self

    def __exit__(self, exc_type: Any, exc: Any, traceback: Any) -> None:
        if self._token is not None:
            reset_scope(self._token)

    def capture(self, kind: str, content: JSON, **metadata: Any) -> Record:
        return self.client.capture(kind, content, conversation_id=self.id, **metadata)

    def message(self, role: str, content: JSON, **metadata: Any) -> Record:
        return self.capture(f"message.{role}", content, **metadata)

    def tool_result(
        self, tool: str, content: JSON, *, status: str = "completed", **metadata: Any
    ) -> Record:
        return self.capture("tool.result", content, tool=tool, status=status, **metadata)

    def batch(self, *, segment_id: str | None = None) -> Batch:
        return Batch(self.client, self.id, segment_id)


class Batch(AbstractContextManager["Batch"]):
    def __init__(self, client: Farfield, conversation_id: str, segment_id: str | None) -> None:
        self.client = client
        self.conversation_id = conversation_id
        self.segment_id = segment_id
        self.events: list[Event] = []
        self.segment: Segment | None = None

    def __enter__(self) -> Batch:
        return self

    def __exit__(self, exc_type: Any, exc: Any, traceback: Any) -> None:
        if exc_type is None and self.events:
            self.segment = self.client.capture_batch(self.events, segment_id=self.segment_id)

    def capture(self, kind: str, content: JSON, **metadata: Any) -> Event:
        event = Event(kind=kind, content=content, conversation_id=self.conversation_id, **metadata)
        self.events.append(event)
        return event

    def message(self, role: str, content: JSON, **metadata: Any) -> Event:
        return self.capture(f"message.{role}", content, **metadata)

    def tool_result(
        self, tool: str, content: JSON, *, status: str = "completed", **metadata: Any
    ) -> Event:
        return self.capture("tool.result", content, tool=tool, status=status, **metadata)


class AsyncFarfield:
    """Async facade with the same durable semantics as :class:`Farfield`."""

    def __init__(self, **options: Any) -> None:
        self._sync = Farfield(**options)

    async def __aenter__(self) -> AsyncFarfield:
        return self

    async def __aexit__(self, exc_type: Any, exc: Any, traceback: Any) -> None:
        return None

    async def capture(self, kind: str, content: JSON, **metadata: Any) -> Record:
        return await asyncio.to_thread(self._sync.capture, kind, content, **metadata)

    async def capture_event(self, event: Event) -> Record:
        return await asyncio.to_thread(self._sync.capture_event, event)

    async def capture_batch(
        self, events: Iterable[Event], *, segment_id: str | None = None
    ) -> Segment:
        values = tuple(events)
        return await asyncio.to_thread(self._sync.capture_batch, values, segment_id=segment_id)

    async def query(self, **filters: Any) -> tuple[Record, ...]:
        return await asyncio.to_thread(self._sync.query, **filters)

    async def get_record(self, record_id: str) -> Entry:
        return await asyncio.to_thread(self._sync.get_record, record_id)

    async def conversations(self, *, limit: int = 100) -> tuple[ConversationSummary, ...]:
        return await asyncio.to_thread(self._sync.conversations, limit=limit)

    async def timeline(self, conversation_id: str) -> tuple[Entry, ...]:
        return await asyncio.to_thread(self._sync.timeline, conversation_id)

    async def health(self) -> bool:
        return await asyncio.to_thread(self._sync.health)

    def conversation(
        self,
        conversation_id: str | None = None,
        *,
        trace_id: str | None = None,
        agent: str | None = None,
        tags: Mapping[str, str] | None = None,
    ) -> AsyncConversation:
        scope = Scope(
            conversation_id=conversation_id or _id("conv_"),
            trace_id=trace_id,
            agent=agent,
            tags=dict(tags or {}),
        )
        return AsyncConversation(self, scope)

    def batch(
        self, conversation_id: str | None = None, *, segment_id: str | None = None
    ) -> AsyncBatch:
        scope = get_scope()
        resolved = conversation_id or (scope.conversation_id if scope else None) or _id("conv_")
        return AsyncBatch(self, resolved, segment_id)

    async def create_run(self, **input: Any) -> RuntimeEvent:
        return await asyncio.to_thread(self._sync.create_run, **input)

    async def get_run(self, run_id: str) -> Run:
        return await asyncio.to_thread(self._sync.get_run, run_id)

    async def run_events(self, run_id: str) -> tuple[RuntimeEvent, ...]:
        return await asyncio.to_thread(self._sync.run_events, run_id)

    async def transition_run(self, run_id: str, to: str, **input: Any) -> RuntimeEvent:
        return await asyncio.to_thread(self._sync.transition_run, run_id, to, **input)

    async def checkpoint_run(self, run_id: str, checkpoint: JSON, **input: Any) -> RuntimeEvent:
        return await asyncio.to_thread(self._sync.checkpoint_run, run_id, checkpoint, **input)


class AsyncConversation(AbstractAsyncContextManager["AsyncConversation"]):
    def __init__(self, client: AsyncFarfield, scope: Scope) -> None:
        self.client = client
        self.scope = scope
        self._token: Any = None

    @property
    def id(self) -> str:
        assert self.scope.conversation_id is not None
        return self.scope.conversation_id

    async def __aenter__(self) -> AsyncConversation:
        self._token = set_scope(_merge_scope(get_scope(), self.scope))
        return self

    async def __aexit__(self, exc_type: Any, exc: Any, traceback: Any) -> None:
        if self._token is not None:
            reset_scope(self._token)

    async def capture(self, kind: str, content: JSON, **metadata: Any) -> Record:
        return await self.client.capture(kind, content, conversation_id=self.id, **metadata)

    async def message(self, role: str, content: JSON, **metadata: Any) -> Record:
        return await self.capture(f"message.{role}", content, **metadata)

    async def tool_result(
        self,
        tool: str,
        content: JSON,
        *,
        status: str = "completed",
        **metadata: Any,
    ) -> Record:
        return await self.capture("tool.result", content, tool=tool, status=status, **metadata)

    def batch(self, *, segment_id: str | None = None) -> AsyncBatch:
        return AsyncBatch(self.client, self.id, segment_id)


class AsyncBatch(AbstractAsyncContextManager["AsyncBatch"]):
    def __init__(self, client: AsyncFarfield, conversation_id: str, segment_id: str | None) -> None:
        self.client = client
        self.conversation_id = conversation_id
        self.segment_id = segment_id
        self.events: list[Event] = []
        self.segment: Segment | None = None

    async def __aenter__(self) -> AsyncBatch:
        return self

    async def __aexit__(self, exc_type: Any, exc: Any, traceback: Any) -> None:
        if exc_type is None and self.events:
            self.segment = await self.client.capture_batch(self.events, segment_id=self.segment_id)

    def capture(self, kind: str, content: JSON, **metadata: Any) -> Event:
        event = Event(kind=kind, content=content, conversation_id=self.conversation_id, **metadata)
        self.events.append(event)
        return event

    def message(self, role: str, content: JSON, **metadata: Any) -> Event:
        return self.capture(f"message.{role}", content, **metadata)

    def tool_result(
        self,
        tool: str,
        content: JSON,
        *,
        status: str = "completed",
        **metadata: Any,
    ) -> Event:
        return self.capture("tool.result", content, tool=tool, status=status, **metadata)


def _id(prefix: str) -> str:
    return prefix + secrets.token_hex(16)


def _encode(value: Any) -> bytes:
    try:
        return json.dumps(
            value, ensure_ascii=False, allow_nan=False, separators=(",", ":")
        ).encode()
    except (TypeError, ValueError) as error:
        raise ValueError(f"farfield: value is not valid JSON: {error}") from error


def _decode(data: bytes) -> Any:
    if not data:
        return None
    try:
        return json.loads(data)
    except json.JSONDecodeError as error:
        raise TransportError(f"farfield: response was not valid JSON: {error}") from error


def _mapping(value: Any) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise TransportError("farfield: expected a JSON object")
    return cast(Mapping[str, Any], value)


def _list(value: Any) -> list[Any]:
    if not isinstance(value, list):
        raise TransportError("farfield: expected a JSON list")
    return cast(list[Any], value)


def _api_error(status: int, data: bytes, *, retryable: bool) -> APIError:
    try:
        value = _mapping(json.loads(data))
        details = _mapping(value.get("error"))
        return APIError(
            status,
            str(details.get("code", "")),
            str(details.get("message", "")) or f"HTTP {status}",
            retryable=retryable or bool(details.get("retryable", False)),
        )
    except (json.JSONDecodeError, TransportError, TypeError):
        message = data.decode(errors="replace").strip() or f"HTTP {status}"
        return APIError(status, "", message, retryable=retryable)


def _retry_after(value: str | None) -> float | None:
    if not value:
        return None
    try:
        seconds = int(value)
        return float(seconds) if seconds >= 0 else None
    except ValueError:
        try:
            parsed = parsedate_to_datetime(value)
            return max(0.0, (parsed - datetime.now(parsed.tzinfo)).total_seconds())
        except (TypeError, ValueError):
            return None


def _merge_scope(base: Scope | None, overlay: Scope | None) -> Scope:
    base = base or Scope()
    overlay = overlay or Scope()
    return Scope(
        conversation_id=overlay.conversation_id or base.conversation_id,
        trace_id=overlay.trace_id or base.trace_id,
        span_id=overlay.span_id or base.span_id,
        parent_id=overlay.parent_id or base.parent_id,
        agent=overlay.agent or base.agent,
        tags={**base.tags, **overlay.tags},
    )
