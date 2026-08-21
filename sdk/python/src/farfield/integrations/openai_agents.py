from __future__ import annotations

import hashlib
import threading
from collections import OrderedDict
from collections.abc import Iterable, Mapping
from dataclasses import dataclass
from datetime import datetime
from typing import Any, cast

from .._client import Farfield
from .._models import Event


@dataclass(frozen=True, slots=True)
class ExporterStats:
    traces: int
    spans: int
    buffered_spans: int
    cached_traces: int
    failed_exports: int
    last_error: str | None


class FarfieldTracingExporter:
    """OpenAI Agents SDK ``TracingExporter`` backed by Farfield History.

    The adapter intentionally uses structural typing and has no mandatory
    dependency on ``openai-agents``. Pass it to that SDK's
    ``BatchTraceProcessor``. OpenAI Agents exports a trace envelope when the
    trace starts and completed spans later. Farfield therefore appends one
    idempotent segment per exported batch instead of waiting for a trace-close
    signal that the exporter API does not provide.
    """

    def __init__(
        self,
        client: Farfield,
        *,
        default_agent: str | None = None,
        max_trace_cache: int = 8192,
    ) -> None:
        if max_trace_cache < 1:
            raise ValueError("farfield: max_trace_cache must be positive")
        self.client = client
        self.default_agent = default_agent
        self.max_trace_cache = max_trace_cache
        self._lock = threading.Lock()
        self._traces: OrderedDict[str, dict[str, Any]] = OrderedDict()
        self._pending: dict[str, list[dict[str, Any]]] = {}
        self._trace_count = 0
        self._span_count = 0
        self._failed_exports = 0
        self._last_error: str | None = None

    def export(self, items: list[Any]) -> None:
        """Export a batch supplied by OpenAI Agents' BatchTraceProcessor."""
        payloads = [_export_payload(item) for item in items]
        ready: dict[str, list[dict[str, Any]]] = {}
        new_traces: set[str] = set()
        with self._lock:
            for payload in payloads:
                if payload.get("object") != "trace":
                    continue
                trace_id = str(payload.get("id", ""))
                if trace_id:
                    self._traces[trace_id] = payload
                    self._traces.move_to_end(trace_id)
                    while len(self._traces) > self.max_trace_cache:
                        self._traces.popitem(last=False)
                    new_traces.add(trace_id)
            for payload in payloads:
                if payload.get("object") != "trace.span":
                    continue
                trace_id = str(payload.get("trace_id", ""))
                if trace_id:
                    self._pending.setdefault(trace_id, []).append(payload)
            # The official processor exports trace envelopes before their
            # spans. If a custom processor violates that order, commit the
            # spans under their trace ID immediately instead of retaining an
            # unbounded orphan buffer.
            for trace_id in list(self._pending):
                ready[trace_id] = self._pending.pop(trace_id)
            for trace_id in new_traces:
                ready.setdefault(trace_id, [])
            traces = {
                trace_id: self._traces.get(trace_id)
                or {
                    "object": "trace",
                    "id": trace_id,
                    "workflow_name": "OpenAI Agent workflow",
                }
                for trace_id in ready
            }
        for trace_id, spans in ready.items():
            self._commit_batch(traces[trace_id], spans, include_trace=trace_id in new_traces)

    def flush(self) -> None:
        """Commit spans whose trace envelope was not observed before shutdown."""
        with self._lock:
            pending = list(self._pending.items())
            self._pending.clear()
            traces = dict(self._traces)
        for trace_id, spans in pending:
            trace = traces.get(trace_id) or {
                "object": "trace",
                "id": trace_id,
                "workflow_name": "OpenAI Agent workflow",
            }
            self._commit_batch(trace, spans, include_trace=trace_id not in traces)

    def shutdown(self) -> None:
        self.flush()
        with self._lock:
            self._traces.clear()

    def stats(self) -> ExporterStats:
        with self._lock:
            return ExporterStats(
                traces=self._trace_count,
                spans=self._span_count,
                buffered_spans=sum(len(values) for values in self._pending.values()),
                cached_traces=len(self._traces),
                failed_exports=self._failed_exports,
                last_error=self._last_error,
            )

    def _commit_batch(
        self,
        trace: dict[str, Any],
        spans: list[dict[str, Any]],
        *,
        include_trace: bool,
    ) -> None:
        trace_id = str(trace.get("id", ""))
        conversation_id = _external_id(str(trace.get("group_id") or trace_id), "conv_openai_")
        workflow_name = str(trace.get("workflow_name") or "OpenAI Agent workflow")
        events: list[Event] = []
        if include_trace:
            events.append(
                Event(
                    id=_record_id("openai_trace_", trace_id),
                    conversation_id=conversation_id,
                    kind="agent.trace",
                    content={
                        "schema": "farfield.openai_agents.trace.v1",
                        "trace": _json_value(trace),
                    },
                    trace_id=trace_id,
                    agent=self.default_agent,
                    tags={"farfield.source": "openai-agents", "workflow": workflow_name[:1024]},
                )
            )
        events.extend(self._span_event(conversation_id, workflow_name, span) for span in spans)
        if not events:
            return
        try:
            prepared = [self.client.prepare_event(event) for event in events]
            segment_seed = "\n".join(sorted(event.id or "" for event in prepared))
            segment_id = "openai_" + hashlib.sha256(segment_seed.encode()).hexdigest()
            self.client.capture_prepared_batch(prepared, segment_id=segment_id)
            with self._lock:
                self._trace_count += int(include_trace)
                self._span_count += len(spans)
        except Exception as error:
            with self._lock:
                self._failed_exports += 1
                self._last_error = str(error)
            raise

    def _span_event(self, conversation_id: str, workflow_name: str, span: dict[str, Any]) -> Event:
        data = _mapping(span.get("span_data"))
        raw_type = str(data.get("type", "custom"))
        custom = _mapping(data.get("data"))
        span_type = str(custom.get("sdk_span_type", raw_type)) if raw_type == "custom" else raw_type
        trace_id = str(span.get("trace_id", ""))
        span_id = str(span.get("id", ""))
        parent_id = _optional_string(span.get("parent_id"))
        error = span.get("error")
        agent = self.default_agent
        tool = None
        if span_type == "agent":
            agent = _optional_string(data.get("name")) or agent
        elif span_type == "turn":
            agent = _optional_string(custom.get("agent_name")) or agent
        if span_type in {"function", "mcp_tools"}:
            tool = _optional_string(data.get("name")) or _optional_string(data.get("server"))
        return Event(
            id=_record_id("openai_span_", span_id),
            conversation_id=conversation_id,
            kind=_span_kind(span_type),
            content={"schema": "farfield.openai_agents.span.v1", "span": _json_value(span)},
            occurred_at=_timestamp(span.get("started_at")),
            trace_id=trace_id or None,
            span_id=span_id or None,
            parent_id=parent_id,
            agent=agent,
            tool=tool,
            status="error" if error else "ok",
            tags={
                "farfield.source": "openai-agents",
                "openai.span.type": span_type[:1024],
                "workflow": workflow_name[:1024],
            },
        )


def _span_kind(span_type: str) -> str:
    return {
        "agent": "agent.invoke",
        "task": "agent.task",
        "turn": "agent.turn",
        "generation": "model.generation",
        "response": "model.generation",
        "function": "tool.execution",
        "mcp_tools": "tool.execution",
        "handoff": "agent.handoff",
        "guardrail": "guardrail",
        "transcription": "voice.transcription",
        "speech": "voice.synthesis",
        "speech_group": "voice.session",
        "custom": "trace.span",
    }.get(span_type, f"openai.{span_type}"[:128])


def _export_payload(item: Any) -> dict[str, Any]:
    exported = item.export()
    if not isinstance(exported, Mapping):
        raise TypeError("farfield: OpenAI Agents trace item export() must return a mapping")
    mapping = cast(Mapping[object, object], exported)
    return {str(key): value for key, value in mapping.items()}


def _record_id(prefix: str, value: str) -> str:
    candidate = prefix + value
    if len(candidate) <= 255 and _valid_id(candidate):
        return candidate
    return prefix + hashlib.sha256(value.encode()).hexdigest()


def _external_id(value: str, prefix: str) -> str:
    if _valid_id(value):
        return value
    return prefix + hashlib.sha256(value.encode()).hexdigest()


def _valid_id(value: str) -> bool:
    return (
        bool(value)
        and len(value) <= 255
        and value[0].isalnum()
        and all(char.isascii() and (char.isalnum() or char in "._:@/-") for char in value)
    )


def _mapping(value: Any) -> dict[str, Any]:
    if not isinstance(value, Mapping):
        return {}
    mapping = cast(Mapping[object, object], value)
    return {str(key): item for key, item in mapping.items()}


def _optional_string(value: Any) -> str | None:
    return value if isinstance(value, str) and value else None


def _timestamp(value: Any) -> datetime | None:
    if not isinstance(value, str) or not value:
        return None
    try:
        return datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return None


def _json_value(value: Any) -> Any:
    if value is None or isinstance(value, (bool, int, float, str)):
        return value
    if isinstance(value, Mapping):
        mapping = cast(Mapping[object, object], value)
        return {str(key): _json_value(item) for key, item in mapping.items()}
    if isinstance(value, Iterable) and not isinstance(value, (str, bytes, bytearray)):
        iterable = cast(Iterable[object], value)
        return [_json_value(item) for item in iterable]
    return str(value)
