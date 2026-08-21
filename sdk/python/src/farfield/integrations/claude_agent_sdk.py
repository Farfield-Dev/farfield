from __future__ import annotations

import asyncio
import hashlib
from collections.abc import Mapping
from typing import Any, cast

from claude_agent_sdk import HookMatcher
from claude_agent_sdk.types import HookContext, HookEvent, HookInput, HookJSONOutput

from .._client import Farfield
from .._models import Event
from .._processor import BackgroundProcessor, ProcessorStats

_EVENTS: tuple[HookEvent, ...] = (
    "PreToolUse",
    "PostToolUse",
    "PostToolUseFailure",
    "UserPromptSubmit",
    "Stop",
    "SubagentStop",
    "PreCompact",
    "Notification",
    "SubagentStart",
    "PermissionRequest",
)


class FarfieldClaudeAgentHooks:
    """Non-blocking History capture hooks for ``claude-agent-sdk``.

    Add :meth:`matchers` to ``ClaudeAgentOptions.hooks`` and call
    :meth:`shutdown` after the SDK client/query completes. Hook callbacks only
    snapshot and enqueue an event; network and object-storage latency remain
    off Claude's execution path.
    """

    def __init__(
        self,
        client: Farfield,
        *,
        processor: BackgroundProcessor | None = None,
        default_agent: str | None = None,
        max_queue_size: int = 8192,
        max_batch_size: int = 128,
        schedule_delay: float = 0.25,
    ) -> None:
        self.client = client
        self.default_agent = default_agent
        self.processor = processor or BackgroundProcessor(
            client,
            max_queue_size=max_queue_size,
            max_batch_size=max_batch_size,
            schedule_delay=schedule_delay,
        )

    def matchers(self, *, timeout: float | None = None) -> dict[HookEvent, list[HookMatcher]]:
        """Return a complete mapping ready for ``ClaudeAgentOptions(hooks=...)``."""
        return {
            event: [HookMatcher(matcher=None, hooks=[self.capture], timeout=timeout)]
            for event in _EVENTS
        }

    async def capture(
        self,
        input_data: HookInput,
        tool_use_id: str | None,
        context: HookContext,
    ) -> HookJSONOutput:
        """Capture one hook notification without changing Claude's behavior."""
        del context
        event = _event(input_data, tool_use_id, self.default_agent)
        self.processor.submit(event)
        return {}

    async def flush(self, *, timeout: float | None = None) -> bool:
        return await asyncio.to_thread(self.processor.flush, timeout)

    async def shutdown(self, *, timeout: float | None = None) -> bool:
        return await asyncio.to_thread(self.processor.shutdown, timeout=timeout)

    def stats(self) -> ProcessorStats:
        return self.processor.stats()


def _event(
    input_data: HookInput, callback_tool_use_id: str | None, default_agent: str | None
) -> Event:
    raw = cast(Mapping[str, object], input_data)
    event_name = str(raw.get("hook_event_name", "Unknown"))
    session_id = str(raw.get("session_id", ""))
    if not session_id:
        raise ValueError("farfield: Claude Agent hook input is missing session_id")
    tool_use_id = _string(raw.get("tool_use_id")) or callback_tool_use_id
    tool = _string(raw.get("tool_name"))
    agent_id = _string(raw.get("agent_id"))
    agent = _string(raw.get("agent_type")) or default_agent
    content = {
        "schema": "farfield.claude_agent_sdk.hook.v1",
        "hook": _json_value(raw),
        "callback_tool_use_id": callback_tool_use_id,
    }
    return Event(
        conversation_id=_external_id(session_id, "conv_claude_"),
        kind=_kind(event_name),
        content=content,
        trace_id=session_id if _valid_id(session_id) else None,
        span_id=_safe_optional_id(tool_use_id or agent_id, "span_claude_"),
        parent_id=_safe_optional_id(agent_id, "span_claude_") if tool_use_id else None,
        agent=agent,
        tool=tool,
        status=_status(event_name),
        tags={
            "farfield.source": "claude-agent-sdk",
            "claude.hook.event": event_name[:1024],
        },
    )


def _kind(event_name: str) -> str:
    return {
        "UserPromptSubmit": "message.user",
        "PreToolUse": "tool.call",
        "PostToolUse": "tool.result",
        "PostToolUseFailure": "tool.result",
        "PermissionRequest": "tool.permission",
        "Stop": "agent.stop",
        "SubagentStart": "agent.subagent.start",
        "SubagentStop": "agent.subagent.stop",
        "PreCompact": "conversation.compact",
        "Notification": "agent.notification",
    }.get(event_name, "agent.hook")


def _status(event_name: str) -> str | None:
    return {
        "PreToolUse": "running",
        "PostToolUse": "ok",
        "PostToolUseFailure": "error",
        "SubagentStart": "running",
        "SubagentStop": "ok",
        "Stop": "ok",
    }.get(event_name)


def _external_id(value: str, prefix: str) -> str:
    if _valid_id(value):
        return value
    return prefix + hashlib.sha256(value.encode()).hexdigest()


def _safe_optional_id(value: str | None, prefix: str) -> str | None:
    return None if value is None else _external_id(value, prefix)


def _valid_id(value: str) -> bool:
    return (
        bool(value)
        and len(value) <= 255
        and value[0].isalnum()
        and all(char.isascii() and (char.isalnum() or char in "._:@/-") for char in value)
    )


def _string(value: object | None) -> str | None:
    return value if isinstance(value, str) and value else None


def _json_value(value: object) -> Any:
    if value is None or isinstance(value, (bool, int, float, str)):
        return value
    if isinstance(value, Mapping):
        mapping = cast(Mapping[object, object], value)
        return {str(key): _json_value(item) for key, item in mapping.items()}
    if isinstance(value, (list, tuple)):
        items = cast(list[object] | tuple[object, ...], value)
        return [_json_value(item) for item in items]
    return str(value)
