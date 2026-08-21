from __future__ import annotations

import asyncio
import json
import threading
import unittest
from collections.abc import Generator
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any
from urllib.parse import urlparse

from farfield import (
    APIError,
    AsyncFarfield,
    BackgroundProcessor,
    DroppedEvent,
    Event,
    Farfield,
    Scope,
)


class _Handler(BaseHTTPRequestHandler):
    requests: list[tuple[str, str, bytes]] = []
    fail_records = 0

    def log_message(self, format: str, *args: Any) -> None:
        return

    def do_GET(self) -> None:
        path = urlparse(self.path).path
        type(self).requests.append(("GET", self.path, b""))
        if path == "/v1/health":
            self._json(200, {"ok": True, "service": "farfield"})
        elif path == "/v1/history/records":
            self._json(
                200,
                [
                    _record(
                        {"id": "rec_query", "conversation_id": "conv_query", "kind": "message.user"}
                    )
                ],
            )
        elif path == "/v1/history/records/rec_query":
            self._json(
                200,
                {
                    "record": _record({"id": "rec_query", "conversation_id": "conv_query"}),
                    "content": {"text": "hello"},
                },
            )
        elif path == "/v1/history/search":
            self._json(
                200,
                {
                    "hits": [
                        {
                            "record": _record({"id": "rec_query", "conversation_id": "conv_query"}),
                            "score": 1.5,
                            "snippet": "hello world",
                        }
                    ],
                    "total": 1,
                    "took_ms": 0.2,
                    "indexed_records": 1,
                    "index_updated_at": "2026-01-01T00:00:00Z",
                },
            )
        elif path == "/v1/history/conversations":
            self._json(
                200,
                [
                    {
                        "id": "conv_query",
                        "record_count": 1,
                        "first_seen_at": "2026-01-01T00:00:00Z",
                        "last_seen_at": "2026-01-01T00:00:00Z",
                        "agents": ["demo"],
                        "kinds": ["message.user"],
                    }
                ],
            )
        elif path == "/v1/history/conversations/conv_query/timeline":
            self._json(
                200,
                [
                    {
                        "record": _record({"id": "rec_query", "conversation_id": "conv_query"}),
                        "content": {"text": "hello"},
                    }
                ],
            )
        else:
            self._error(404, "FH_NOT_FOUND", "not found")

    def do_POST(self) -> None:
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        type(self).requests.append(("POST", self.path, body))
        if self.path == "/v1/history/records":
            if json.loads(body).get("conversation_id") == "error":
                self._error(409, "FH_CONFLICT", "already exists")
                return
            if type(self).fail_records > 0:
                type(self).fail_records -= 1
                self._error(503, "FH_BUSY", "try again", retryable=True)
                return
            self._json(201, _record(json.loads(body)))
        elif self.path == "/v1/history/segments":
            value = json.loads(body)
            records = value["records"]
            self._json(
                201,
                {
                    "schema_version": "farfield.history.segment.v1",
                    "id": value["id"],
                    "conversation_id": records[0]["conversation_id"],
                    "entries": [
                        {"record": _record(item), "content": item["content"]} for item in records
                    ],
                    "segment_sha256": "b" * 64,
                },
            )
        elif self.path == "/error":
            self._error(409, "FH_CONFLICT", "already exists")
        else:
            self._error(404, "FH_NOT_FOUND", "not found")

    def _error(self, status: int, code: str, message: str, *, retryable: bool = False) -> None:
        self._json(status, {"error": {"code": code, "message": message, "retryable": retryable}})

    def _json(self, status: int, value: Any) -> None:
        body = json.dumps(value).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def _record(value: dict[str, Any]) -> dict[str, Any]:
    return {
        "schema_version": "farfield.history.record.v2",
        "occurred_at": "2026-01-01T00:00:00Z",
        "recorded_at": "2026-01-01T00:00:00Z",
        "record_sha256": "a" * 64,
        "tags": {},
        "kind": "message.user",
        **value,
    }


@contextmanager
def test_server() -> Generator[tuple[str, type[_Handler]], None, None]:
    handler = type("Handler", (_Handler,), {"requests": [], "fail_records": 0})
    server = ThreadingHTTPServer(("127.0.0.1", 0), handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        address = server.server_address
        host, port = address[0], address[1]
        yield f"http://{host}:{port}", handler
    finally:
        server.shutdown()
        server.server_close()
        thread.join()


class FarfieldTests(unittest.TestCase):
    def test_capture_retries_the_exact_body(self) -> None:
        with test_server() as (endpoint, handler):
            handler.fail_records = 1
            client = Farfield(
                endpoint=endpoint,
                retry_delay=0,
                defaults=Scope(agent="planner", tags={"env": "test"}),
            )
            record = client.capture("message.user", {"text": "hello"}, conversation_id="conv_one")

        self.assertTrue(record.id.startswith("rec_"))
        requests = [item for item in handler.requests if item[1] == "/v1/history/records"]
        self.assertEqual(2, len(requests))
        self.assertEqual(requests[0][2], requests[1][2])
        payload = json.loads(requests[0][2])
        self.assertEqual("planner", payload["agent"])
        self.assertEqual({"env": "test"}, payload["tags"])

    def test_conversation_and_batch_are_scoped(self) -> None:
        with test_server() as (endpoint, handler):
            client = Farfield(endpoint=endpoint, max_retries=0)
            with client.conversation(
                "conv_scope", trace_id="trace_one", tags={"tenant": "acme"}
            ) as conversation:
                conversation.message("user", "hello")
                with conversation.batch(segment_id="seg_one") as batch:
                    batch.message("assistant", "hi")
                    batch.tool_result("weather", {"sunny": True})
            self.assertIsNotNone(batch.segment)

        bodies = [json.loads(body) for method, _, body in handler.requests if method == "POST"]
        self.assertEqual("trace_one", bodies[0]["trace_id"])
        self.assertEqual("conv_scope", bodies[1]["records"][0]["conversation_id"])
        self.assertEqual("seg_one", bodies[1]["id"])

    def test_before_send_can_redact_or_drop(self) -> None:
        def redact(event: Event) -> Event | None:
            if event.kind == "debug":
                return None
            return event.with_updates(content="[redacted]")

        with test_server() as (endpoint, handler):
            client = Farfield(endpoint=endpoint, before_send=redact)
            client.capture("message.user", "secret", conversation_id="conv_one")
            with self.assertRaises(DroppedEvent):
                client.capture("debug", "noise", conversation_id="conv_one")

        self.assertEqual("[redacted]", json.loads(handler.requests[0][2])["content"])
        self.assertEqual(1, len(handler.requests))

    def test_invalid_json_is_rejected_before_transport(self) -> None:
        with test_server() as (endpoint, handler):
            client = Farfield(endpoint=endpoint)
            with self.assertRaises(ValueError):
                client.capture("metric", float("nan"), conversation_id="conv_one")
        self.assertEqual([], handler.requests)

    def test_background_processor_batches_by_conversation_and_flushes(self) -> None:
        with test_server() as (endpoint, handler):
            client = Farfield(endpoint=endpoint, defaults=Scope(tags={"env": "test"}))
            processor = BackgroundProcessor(client, max_batch_size=10, schedule_delay=0.01)
            with client.conversation("conv_one", agent="researcher"):
                self.assertTrue(processor.submit(Event("message.user", "hello")))
                self.assertTrue(processor.submit(Event("message.assistant", "hi")))
            self.assertTrue(
                processor.submit(
                    Event("tool.result", {"ok": True}, conversation_id="conv_two", tool="search")
                )
            )
            self.assertTrue(processor.flush(timeout=2))
            self.assertTrue(processor.shutdown())

        requests = [
            json.loads(body) for _, path, body in handler.requests if path == "/v1/history/segments"
        ]
        self.assertEqual(2, len(requests))
        groups = {
            request["conversation_id"]
            if "conversation_id" in request
            else request["records"][0]["conversation_id"]: request
            for request in requests
        }
        self.assertEqual(2, len(groups["conv_one"]["records"]))
        self.assertEqual("researcher", groups["conv_one"]["records"][0]["agent"])
        self.assertEqual({"env": "test"}, groups["conv_one"]["records"][0]["tags"])
        stats = processor.stats()
        self.assertEqual(3, stats.enqueued)
        self.assertEqual(3, stats.committed)
        self.assertEqual(0, stats.pending)
        self.assertEqual(2, stats.batches)

    def test_background_processor_reports_delivery_failure(self) -> None:
        client = Farfield(endpoint="http://127.0.0.1:1", max_retries=0, timeout=0.05)
        errors: list[Exception] = []
        processor = BackgroundProcessor(client, schedule_delay=0, on_error=errors.append)
        self.assertTrue(
            processor.submit(Event("message.user", "hello", conversation_id="conv_fail"))
        )
        self.assertFalse(processor.flush(timeout=2))
        self.assertFalse(processor.shutdown())
        stats = processor.stats()
        self.assertEqual(1, stats.failed)
        self.assertEqual(0, stats.committed)
        self.assertEqual(1, len(errors))

    def test_read_surface(self) -> None:
        with test_server() as (endpoint, handler):
            client = Farfield(endpoint=endpoint)
            self.assertTrue(client.health())
            self.assertEqual(
                "rec_query",
                client.query(conversation_id="conv_query", tags={"env": "test"}, limit=10)[0].id,
            )
            self.assertTrue(any("tag=env%3Dtest" in path for _, path, _ in handler.requests))
            self.assertEqual(
                "rec_query", client.search("hello", tags={"env": "test"}).hits[0].record.id
            )
            content = client.get_record("rec_query").content
            assert isinstance(content, dict)
            self.assertEqual("hello", content["text"])
            self.assertEqual("conv_query", client.conversations()[0].id)
            self.assertEqual("rec_query", client.timeline("conv_query")[0].record.id)

    def test_errors_are_typed(self) -> None:
        with test_server() as (endpoint, _):
            client = Farfield(endpoint=endpoint, max_retries=0)
            with self.assertRaises(APIError) as raised:
                client.capture("message.user", None, conversation_id="error")
        self.assertEqual(409, raised.exception.status_code)
        self.assertEqual("FH_CONFLICT", raised.exception.code)

    def test_async_conversation_context_is_task_local(self) -> None:
        async def run(endpoint: str) -> None:
            client = AsyncFarfield(endpoint=endpoint)

            async def capture(conversation_id: str) -> None:
                async with client.conversation(conversation_id):
                    await asyncio.sleep(0)
                    await client.capture("message.user", conversation_id)

            await asyncio.gather(capture("conv_a"), capture("conv_b"))

        with test_server() as (endpoint, handler):
            asyncio.run(run(endpoint))
        values = {json.loads(body)["conversation_id"] for _, _, body in handler.requests}
        self.assertEqual({"conv_a", "conv_b"}, values)


if __name__ == "__main__":
    unittest.main()
