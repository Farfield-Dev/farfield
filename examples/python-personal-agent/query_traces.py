from __future__ import annotations

import argparse
import json
import os
from dataclasses import asdict
from pathlib import Path
from typing import Any

from farfield import Farfield

HERE = Path(__file__).resolve().parent


def read_runs(path: Path) -> list[dict[str, Any]]:
    if not path.exists():
        return []
    return [json.loads(line) for line in path.read_text().splitlines() if line.strip()]


def main() -> int:
    parser = argparse.ArgumentParser(description="Query retained personal-agent traces")
    parser.add_argument("--conversation")
    parser.add_argument("--run")
    parser.add_argument(
        "--endpoint", default=os.getenv("FARFIELD_ENDPOINT", "http://127.0.0.1:8787")
    )
    parser.add_argument("--runs-file", type=Path, default=HERE / "test-runs.jsonl")
    args = parser.parse_args()
    ff = Farfield(endpoint=args.endpoint, timeout=60)
    runs = read_runs(args.runs_file)

    if args.run:
        print(
            json.dumps(
                {
                    "run": ff.get_run(args.run).raw,
                    "events": [event.raw for event in ff.run_events(args.run)],
                },
                indent=2,
            )
        )
        return 0

    conversation_id = args.conversation
    if not conversation_id and runs:
        conversation_id = str(runs[-1]["conversation_id"])
    if not conversation_id:
        print(
            json.dumps(
                {
                    "runs": runs,
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
        "runs": [item for item in runs if item.get("conversation_id") == conversation_id],
    }
    print(json.dumps(result, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
