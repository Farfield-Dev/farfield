from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass, field, replace
from datetime import datetime, timezone
from typing import Any, TypeAlias, Union, cast

JSON: TypeAlias = Union[None, bool, int, float, str, list["JSON"], dict[str, "JSON"]]


def _empty_tags() -> dict[str, str]:
    return {}


def _empty_raw() -> dict[str, Any]:
    return {}


@dataclass(frozen=True, slots=True)
class Scope:
    conversation_id: str | None = None
    trace_id: str | None = None
    span_id: str | None = None
    parent_id: str | None = None
    agent: str | None = None
    tags: dict[str, str] = field(default_factory=_empty_tags)


@dataclass(frozen=True, slots=True)
class Event:
    kind: str
    content: JSON
    conversation_id: str | None = None
    id: str | None = None
    occurred_at: datetime | None = None
    sequence: int | None = None
    trace_id: str | None = None
    span_id: str | None = None
    parent_id: str | None = None
    agent: str | None = None
    tool: str | None = None
    status: str | None = None
    tags: dict[str, str] = field(default_factory=_empty_tags)

    def with_updates(self, **changes: Any) -> Event:
        return replace(self, **changes)

    def to_payload(self) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "id": self.id,
            "conversation_id": self.conversation_id,
            "kind": self.kind,
            "content": self.content,
            "occurred_at": _timestamp(self.occurred_at),
            "sequence": self.sequence,
            "trace_id": self.trace_id,
            "span_id": self.span_id,
            "parent_id": self.parent_id,
            "agent": self.agent,
            "tool": self.tool,
            "status": self.status,
            "tags": dict(self.tags),
        }
        return {key: value for key, value in payload.items() if value is not None}


@dataclass(frozen=True, slots=True)
class Record:
    id: str
    conversation_id: str
    kind: str
    schema_version: str | None = None
    occurred_at: str | None = None
    recorded_at: str | None = None
    record_sha256: str | None = None
    sequence: int | None = None
    trace_id: str | None = None
    span_id: str | None = None
    parent_id: str | None = None
    agent: str | None = None
    tool: str | None = None
    status: str | None = None
    tags: dict[str, str] = field(default_factory=_empty_tags)
    raw: dict[str, Any] = field(default_factory=_empty_raw, repr=False)

    @classmethod
    def from_payload(cls, value: Mapping[str, Any]) -> Record:
        return cls(
            id=str(value.get("id", "")),
            conversation_id=str(value.get("conversation_id", "")),
            kind=str(value.get("kind", "")),
            schema_version=_optional_string(value.get("schema_version")),
            occurred_at=_optional_string(value.get("occurred_at")),
            recorded_at=_optional_string(value.get("recorded_at")),
            record_sha256=_optional_string(value.get("record_sha256")),
            sequence=_optional_int(value.get("sequence")),
            trace_id=_optional_string(value.get("trace_id")),
            span_id=_optional_string(value.get("span_id")),
            parent_id=_optional_string(value.get("parent_id")),
            agent=_optional_string(value.get("agent")),
            tool=_optional_string(value.get("tool")),
            status=_optional_string(value.get("status")),
            tags=_string_mapping(value.get("tags")),
            raw=dict(value),
        )


@dataclass(frozen=True, slots=True)
class Entry:
    record: Record
    content: JSON

    @classmethod
    def from_payload(cls, value: Mapping[str, Any]) -> Entry:
        return cls(
            record=Record.from_payload(_mapping(value.get("record"))), content=value.get("content")
        )


@dataclass(frozen=True, slots=True)
class ConversationSummary:
    id: str
    record_count: int
    first_seen_at: str
    last_seen_at: str
    agents: tuple[str, ...] = ()
    kinds: tuple[str, ...] = ()

    @classmethod
    def from_payload(cls, value: Mapping[str, Any]) -> ConversationSummary:
        return cls(
            id=str(value.get("id", "")),
            record_count=int(value.get("record_count", 0)),
            first_seen_at=str(value.get("first_seen_at", "")),
            last_seen_at=str(value.get("last_seen_at", "")),
            agents=_string_tuple(value.get("agents")),
            kinds=_string_tuple(value.get("kinds")),
        )


@dataclass(frozen=True, slots=True)
class SearchHit:
    record: Record
    score: float
    snippet: str | None = None

    @classmethod
    def from_payload(cls, value: Mapping[str, Any]) -> SearchHit:
        return cls(
            record=Record.from_payload(_mapping(value.get("record"))),
            score=float(value.get("score", 0)),
            snippet=_optional_string(value.get("snippet")),
        )


@dataclass(frozen=True, slots=True)
class SearchResult:
    hits: tuple[SearchHit, ...]
    total: int
    took_ms: float
    indexed_records: int
    index_updated_at: str

    @classmethod
    def from_payload(cls, value: Mapping[str, Any]) -> SearchResult:
        return cls(
            hits=tuple(SearchHit.from_payload(_mapping(item)) for item in _list(value.get("hits"))),
            total=int(value.get("total", 0)),
            took_ms=float(value.get("took_ms", 0)),
            indexed_records=int(value.get("indexed_records", 0)),
            index_updated_at=str(value.get("index_updated_at", "")),
        )


@dataclass(frozen=True, slots=True)
class Segment:
    id: str
    conversation_id: str
    entries: tuple[Entry, ...]
    schema_version: str | None = None
    segment_sha256: str | None = None
    raw: dict[str, Any] = field(default_factory=_empty_raw, repr=False)

    @classmethod
    def from_payload(cls, value: Mapping[str, Any]) -> Segment:
        entries = _list(value.get("entries"))
        return cls(
            id=str(value.get("id", "")),
            conversation_id=str(value.get("conversation_id", "")),
            entries=tuple(Entry.from_payload(_mapping(item)) for item in entries),
            schema_version=_optional_string(value.get("schema_version")),
            segment_sha256=_optional_string(value.get("segment_sha256")),
            raw=dict(value),
        )


def _timestamp(value: datetime | None) -> str | None:
    if value is None:
        return None
    if value.tzinfo is None or value.utcoffset() is None:
        raise ValueError("farfield: occurred_at must be timezone-aware")
    return value.astimezone(timezone.utc).isoformat(timespec="microseconds").replace("+00:00", "Z")


def _optional_string(value: Any) -> str | None:
    return None if value is None else str(value)


def _optional_int(value: Any) -> int | None:
    return None if value is None else int(value)


def _mapping(value: Any) -> Mapping[str, Any]:
    return cast(Mapping[str, Any], value) if isinstance(value, Mapping) else {}


def _string_mapping(value: Any) -> dict[str, str]:
    return {str(key): str(item) for key, item in _mapping(value).items()}


def _string_tuple(value: Any) -> tuple[str, ...]:
    return tuple(str(item) for item in cast(list[Any], value)) if isinstance(value, list) else ()


def _list(value: Any) -> list[Any]:
    return cast(list[Any], value) if isinstance(value, list) else []
