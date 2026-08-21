from __future__ import annotations

import argparse
import json
import os
import sys
import uuid
from collections.abc import Mapping
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from anthropic import Anthropic
from dotenv import load_dotenv
from farfield import Farfield

MODEL = "claude-sonnet-4-6"
AGENT = "farfield-personal-researcher"
HERE = Path(__file__).resolve().parent
REPOSITORY = HERE.parents[1]
DEFAULT_TRACES_FILE = HERE / "test-traces.jsonl"
SYSTEM_PROMPT = """You are a careful personal research agent helping build Farfield.
Use web search when current facts or primary sources matter. Prefer official documentation,
original research, and first-party announcements. Cite sources in the response. Be explicit
about uncertainty, and produce concrete recommendations rather than generic observations.
"""


def utc_now() -> str:
    return datetime.now(UTC).isoformat(timespec="seconds").replace("+00:00", "Z")


def identifier(prefix: str) -> str:
    return f"{prefix}{datetime.now(UTC):%Y%m%dT%H%M%SZ}_{uuid.uuid4().hex[:10]}"


def json_value(value: Any) -> Any:
    """Convert Anthropic SDK response models into ordinary JSON values."""
    if hasattr(value, "model_dump"):
        return value.model_dump(mode="json", exclude_none=True)
    if isinstance(value, Mapping):
        return {str(key): json_value(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [json_value(item) for item in value]
    if value is None or isinstance(value, (bool, int, float, str)):
        return value
    return str(value)


def block_kind(block: Mapping[str, Any]) -> tuple[str, str | None, str | None]:
    block_type = str(block.get("type", "unknown"))
    if block_type == "text":
        return "message.assistant", None, "completed"
    if block_type == "server_tool_use":
        return "tool.call", str(block.get("name", "web_search")), "started"
    if block_type == "web_search_tool_result":
        content = block.get("content")
        failed = (
            isinstance(content, Mapping) and content.get("type") == "web_search_tool_result_error"
        )
        return "tool.result", "web_search", "failed" if failed else "completed"
    return f"anthropic.{block_type}", None, None


def load_prompts(path: Path) -> list[dict[str, str]]:
    value = json.loads(path.read_text())
    if not isinstance(value, list):
        raise ValueError(f"{path} must contain a JSON array")
    prompts: list[dict[str, str]] = []
    for index, item in enumerate(value):
        if (
            not isinstance(item, dict)
            or not isinstance(item.get("name"), str)
            or not isinstance(item.get("prompt"), str)
        ):
            raise ValueError(f"prompt {index} must have string name and prompt fields")
        prompts.append({"name": item["name"], "prompt": item["prompt"]})
    return prompts


def append_manifest(path: Path, value: Mapping[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a", encoding="utf-8") as output:
        output.write(json.dumps(dict(value), sort_keys=True) + "\n")


def run_turn(
    *,
    anthropic: Anthropic,
    farfield: Farfield,
    conversation_id: str,
    trace_id: str,
    name: str,
    prompt: str,
    messages: list[dict[str, Any]],
    model: str,
    max_tokens: int,
    max_searches: int,
    traces_file: Path,
) -> dict[str, Any]:
    turn_id = identifier("turn_agent_")
    started_at = utc_now()

    with farfield.conversation(
        conversation_id,
        trace_id=trace_id,
        agent=AGENT,
        tags={"example": "python-personal-agent", "prompt": name},
    ) as conversation:
        with conversation.batch(segment_id=f"seg_{turn_id}_request") as batch:
            batch.capture(
                "agent.turn.started",
                {"turn_id": turn_id, "prompt_name": name, "started_at": started_at},
                status="running",
            )
            batch.message("user", {"text": prompt, "turn_id": turn_id})
            batch.capture(
                "model.request",
                {
                    "provider": "anthropic",
                    "model": model,
                    "max_tokens": max_tokens,
                    "tools": [{"name": "web_search", "max_uses": max_searches}],
                    "message_count": len(messages) + 1,
                },
                status="started",
            )

        messages.append({"role": "user", "content": prompt})
        try:
            response = anthropic.messages.create(
                model=model,
                max_tokens=max_tokens,
                system=SYSTEM_PROMPT,
                messages=messages,
                tools=[
                    {
                        "type": "web_search_20250305",
                        "name": "web_search",
                        "max_uses": max_searches,
                    }
                ],
            )
            response_value = json_value(response)
            content = response_value.get("content", [])
            if not isinstance(content, list):
                content = []
            assistant_text = "".join(
                str(block.get("text", ""))
                for block in content
                if isinstance(block, Mapping) and block.get("type") == "text"
            )
            citations = [
                citation
                for block in content
                if isinstance(block, Mapping) and block.get("type") == "text"
                for citation in block.get("citations", [])
                if isinstance(citation, Mapping)
            ]

            with conversation.batch(segment_id=f"seg_{turn_id}_response") as batch:
                for block in content:
                    if not isinstance(block, Mapping):
                        continue
                    kind, tool, status = block_kind(block)
                    if kind not in {"message.assistant"}:
                        batch.capture(kind, dict(block), tool=tool, status=status)
                batch.message(
                    "assistant",
                    {"text": assistant_text, "citations": citations},
                    status="completed",
                )
                batch.capture(
                    "model.response",
                    response_value,
                    status="completed",
                )
                batch.capture(
                    "agent.turn.completed",
                    {
                        "turn_id": turn_id,
                        "prompt_name": name,
                        "stop_reason": response_value.get("stop_reason"),
                        "usage": response_value.get("usage"),
                    },
                    status="completed",
                )

            # Farfield retains the lossless provider response above. The next model
            # turn only needs normalized text; replaying encrypted web-search blocks
            # makes context and cost grow dramatically without improving continuity.
            messages.append({"role": "assistant", "content": assistant_text})
            result = {
                "turn_id": turn_id,
                "conversation_id": conversation_id,
                "trace_id": trace_id,
                "prompt_name": name,
                "model": model,
                "status": "completed",
                "started_at": started_at,
                "completed_at": utc_now(),
                "response_id": response.id,
                "usage": json_value(response.usage),
            }
            append_manifest(traces_file, result)
            print(f"\n## {name}\n\n{assistant_text.strip()}\n")
            print(f"Farfield: conversation={conversation_id} trace={trace_id}", file=sys.stderr)
            return result
        except Exception as error:
            error_value = {"type": type(error).__name__, "message": str(error), "turn_id": turn_id}
            try:
                conversation.capture("agent.turn.failed", error_value, status="failed")
            finally:
                append_manifest(
                    traces_file,
                    {
                        "turn_id": turn_id,
                        "conversation_id": conversation_id,
                        "trace_id": trace_id,
                        "prompt_name": name,
                        "model": model,
                        "status": "failed",
                        "started_at": started_at,
                        "completed_at": utc_now(),
                        "error": error_value,
                    },
                )
            raise


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    prompts = parser.add_mutually_exclusive_group()
    prompts.add_argument("--prompt", help="run one custom prompt")
    prompts.add_argument("--suite", action="store_true", help="run every prompt in prompts.json")
    parser.add_argument("--model", default=os.getenv("ANTHROPIC_MODEL", MODEL))
    parser.add_argument(
        "--endpoint", default=os.getenv("FARFIELD_ENDPOINT", "http://127.0.0.1:8787")
    )
    parser.add_argument("--conversation", help="reuse a specific Farfield conversation ID")
    parser.add_argument("--max-tokens", type=int, default=1400)
    parser.add_argument("--max-searches", type=int, default=3)
    parser.add_argument("--traces-file", type=Path, default=DEFAULT_TRACES_FILE)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    load_dotenv(REPOSITORY / ".env", override=False)
    if not os.getenv("ANTHROPIC_API_KEY"):
        print(f"ANTHROPIC_API_KEY is missing; add it to {REPOSITORY / '.env'}", file=sys.stderr)
        return 2
    if args.max_tokens < 1 or args.max_searches < 1:
        print("--max-tokens and --max-searches must be positive", file=sys.stderr)
        return 2

    prompts = (
        load_prompts(HERE / "prompts.json")
        if args.suite
        else [
            {
                "name": "custom",
                "prompt": args.prompt
                or (
                    "Research today's most important agent engineering development "
                    "and explain why it matters."
                ),
            }
        ]
    )
    conversation_id = args.conversation or identifier("conv_personal_")
    trace_id = identifier("trace_personal_")
    farfield = Farfield(endpoint=args.endpoint, timeout=60, max_retries=4)
    if not farfield.health():
        print(f"Farfield is not healthy at {args.endpoint}", file=sys.stderr)
        return 2

    anthropic = Anthropic(timeout=180.0, max_retries=2)
    messages: list[dict[str, Any]] = []
    print(f"Farfield conversation: {conversation_id}", file=sys.stderr)
    for item in prompts:
        run_turn(
            anthropic=anthropic,
            farfield=farfield,
            conversation_id=conversation_id,
            trace_id=trace_id,
            name=item["name"],
            prompt=item["prompt"],
            messages=messages,
            model=args.model,
            max_tokens=args.max_tokens,
            max_searches=args.max_searches,
            traces_file=args.traces_file,
        )
    print(
        f"Inspect: uv run python query_traces.py --conversation {conversation_id}",
        file=sys.stderr,
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
