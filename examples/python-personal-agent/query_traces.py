from __future__ import annotations

import argparse
import json
import os
from dataclasses import asdict
from pathlib import Path
from typing import Any

from farfield import Farfield

HERE = Path(__file__).resolve().parent


def read_traces(path: Path) -> list[dict[str, Any]]:
    if not path.exists():
        return []
    return [json.loads(line) for line in path.read_text().splitlines() if line.strip()]


def main() -> int:
    parser = argparse.ArgumentParser(description="Query retained personal-agent traces")
    parser.add_argument("--conversation")
    parser.add_argument(
        "--search", help="full-text query, for example: '\"object storage\" latency*'"
    )
    parser.add_argument("--agent", help="exact agent filter for --search")
    parser.add_argument("--kind", help="exact record-kind filter for --search")
    parser.add_argument(
        "--endpoint", default=os.getenv("FARFIELD_ENDPOINT", "http://127.0.0.1:8787")
    )
    parser.add_argument("--traces-file", type=Path, default=HERE / "test-traces.jsonl")
    args = parser.parse_args()
    ff = Farfield(endpoint=args.endpoint, timeout=60)
    traces = read_traces(args.traces_file)

    if args.search:
        print(
            json.dumps(
                asdict(ff.search(args.search, agent=args.agent, kind=args.kind, limit=50)),
                indent=2,
            )
        )
        return 0

    conversation_id = args.conversation
    if not conversation_id and traces:
        conversation_id = str(traces[-1]["conversation_id"])
    if not conversation_id:
        print(
            json.dumps(
                {
                    "traces": traces,
                    "conversations": [asdict(item) for item in ff.conversations(limit=20)],
                },
                indent=2,
            )
        )
        return 0

    timeline = ff.timeline(conversation_id)
    result = {
        "conversation_id": conversation_id,
        "records": len(timeline),
        "kinds": [entry.record.kind for entry in timeline],
        "timeline": [{"record": entry.record.raw, "content": entry.content} for entry in timeline],
        "traces": [item for item in traces if item.get("conversation_id") == conversation_id],
    }
    print(json.dumps(result, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
