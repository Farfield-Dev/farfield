from __future__ import annotations

import asyncio
import json
import unittest
from typing import cast

from claude_agent_sdk.types import HookContext, HookInput
from test_client import test_server

from farfield import Farfield
from farfield.integrations.claude_agent_sdk import FarfieldClaudeAgentHooks


class ClaudeAgentSDKIntegrationTests(unittest.TestCase):
    def test_real_hook_matchers_capture_tool_lifecycle(self) -> None:
        async def run(endpoint: str) -> FarfieldClaudeAgentHooks:
            integration = FarfieldClaudeAgentHooks(
                Farfield(endpoint=endpoint), default_agent="claude-code", schedule_delay=0.01
            )
            matchers = integration.matchers(timeout=2)
            context = cast(HookContext, {"signal": None})
            common = {
                "session_id": "session_claude_agent",
                "transcript_path": "/tmp/transcript.jsonl",
                "cwd": "/workspace",
            }
            prompt = cast(
                HookInput,
                {**common, "hook_event_name": "UserPromptSubmit", "prompt": "Inspect the repo"},
            )
            pre = cast(
                HookInput,
                {
                    **common,
                    "hook_event_name": "PreToolUse",
                    "tool_name": "Read",
                    "tool_input": {"file_path": "README.md"},
                    "tool_use_id": "toolu_01",
                },
            )
            post = cast(
                HookInput,
                {
                    **common,
                    "hook_event_name": "PostToolUse",
                    "tool_name": "Read",
                    "tool_input": {"file_path": "README.md"},
                    "tool_response": {"content": "Farfield"},
                    "tool_use_id": "toolu_01",
                },
            )
            await matchers["UserPromptSubmit"][0].hooks[0](prompt, None, context)
            await matchers["PreToolUse"][0].hooks[0](pre, "toolu_01", context)
            await matchers["PostToolUse"][0].hooks[0](post, "toolu_01", context)
            self.assertTrue(await integration.shutdown(timeout=2))
            return integration

        with test_server() as (endpoint, handler):
            integration = asyncio.run(run(endpoint))

        segments = [
            json.loads(body)
            for method, path, body in handler.requests
            if method == "POST" and path == "/v1/history/segments"
        ]
        records = [record for segment in segments for record in segment["records"]]
        self.assertEqual(3, len(records))
        self.assertEqual({"session_claude_agent"}, {item["conversation_id"] for item in records})
        self.assertEqual(
            {"message.user", "tool.call", "tool.result"}, {item["kind"] for item in records}
        )
        tool_result = next(item for item in records if item["kind"] == "tool.result")
        self.assertEqual("Read", tool_result["tool"])
        self.assertEqual("farfield.claude_agent_sdk.hook.v1", tool_result["content"]["schema"])
        self.assertEqual("Farfield", tool_result["content"]["hook"]["tool_response"]["content"])
        self.assertEqual(3, integration.stats().committed)


if __name__ == "__main__":
    unittest.main()
