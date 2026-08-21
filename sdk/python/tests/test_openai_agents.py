from __future__ import annotations

import json
import unittest
from typing import cast

from agents.tracing import (
    agent_span,
    function_span,
    generation_span,
    set_trace_processors,
    trace,
)
from agents.tracing.processor_interface import TracingExporter
from agents.tracing.processors import BatchTraceProcessor
from test_client import test_server

from farfield import Farfield
from farfield.integrations.openai_agents import FarfieldTracingExporter


class OpenAIAgentsIntegrationTests(unittest.TestCase):
    def tearDown(self) -> None:
        set_trace_processors([])

    def test_real_sdk_trace_lifecycle_is_captured(self) -> None:
        with test_server() as (endpoint, handler):
            exporter = FarfieldTracingExporter(Farfield(endpoint=endpoint), default_agent="demo")
            processor = BatchTraceProcessor(cast(TracingExporter, exporter), schedule_delay=60)
            set_trace_processors([processor])

            with (
                trace(
                    "research workflow",
                    trace_id="trace_0123456789abcdef0123456789abcdef",
                    group_id="conversation_openai_agents",
                    metadata={"tenant": "test"},
                ),
                agent_span("researcher", span_id="span_0123456789abcdef0123456789abcdef"),
            ):
                with generation_span(
                    input=[{"role": "user", "content": "Find an answer"}],
                    output=[{"role": "assistant", "content": "I found it"}],
                    model="gpt-test",
                    usage={"input_tokens": 4, "output_tokens": 3},
                    span_id="span_1123456789abcdef0123456789abcdef",
                ):
                    pass
                with function_span(
                    "web_search",
                    input='{"q":"Farfield"}',
                    output='{"results":1}',
                    span_id="span_2123456789abcdef0123456789abcdef",
                ):
                    pass

            processor.force_flush()
            processor.shutdown()
            exporter.shutdown()

        segments = [
            json.loads(body)
            for method, path, body in handler.requests
            if method == "POST" and path == "/v1/history/segments"
        ]
        self.assertGreaterEqual(len(segments), 1)
        records = [record for segment in segments for record in segment["records"]]
        self.assertEqual(4, len(records))
        conversation_ids = {item["conversation_id"] for item in records}
        self.assertEqual({"conversation_openai_agents"}, conversation_ids)
        self.assertEqual(
            {"agent.trace", "agent.invoke", "model.generation", "tool.execution"},
            {item["kind"] for item in records},
        )
        model = next(item for item in records if item["kind"] == "model.generation")
        self.assertEqual("gpt-test", model["content"]["span"]["span_data"]["model"])
        tool = next(item for item in records if item["kind"] == "tool.execution")
        self.assertEqual("web_search", tool["tool"])
        self.assertEqual("farfield.openai_agents.span.v1", tool["content"]["schema"])
        self.assertEqual(1, exporter.stats().traces)
        self.assertEqual(3, exporter.stats().spans)
        self.assertEqual(0, exporter.stats().cached_traces)


if __name__ == "__main__":
    unittest.main()
