# Farfield Python SDK

Durable agent history with Python-native sync and async APIs.

```bash
pip install farfield
```

Start a local Farfield server, then capture and read a conversation:

```python
from farfield import Farfield

ff = Farfield()  # FARFIELD_ENDPOINT and FARFIELD_TOKEN are also supported

with ff.conversation(agent="support-agent", tags={"env": "dev"}) as conversation:
    conversation.message("user", {"text": "Where is my order?"})
    conversation.message("assistant", {"text": "Let me check."})

timeline = ff.timeline(conversation.id)
print([(entry.record.kind, entry.content) for entry in timeline])

hits = ff.search('"order shipped" lookup*', agent="support-agent", tags={"env": "prod"})
print([(hit.record.id, hit.score, hit.snippet) for hit in hits.hits])
```

Every successful write has been acknowledged by Farfield after its authoritative
object-store commit. IDs and timestamps are generated before the first request,
and retries reuse the exact same body.

## Batch related events into one durable segment

```python
with ff.conversation("conv_123") as conversation:
    with conversation.batch() as batch:
        batch.message("user", "Search for a flight")
        batch.capture("tool.call", {"airport": "SFO"}, tool="flight_search")
        batch.tool_result("flight_search", {"flights": 12})

    print(batch.segment.id)
```

## Async

```python
from farfield import AsyncFarfield

ff = AsyncFarfield()

async with ff.conversation("conv_123") as conversation:
    await conversation.message("user", "Hello")
```

Conversation metadata is task-local through `contextvars`, so concurrent async
tasks do not leak IDs or tags into one another.

## Background capture

Use the bounded processor when capture must stay off the agent's hot path:

```python
from farfield import BackgroundProcessor, Event

processor = BackgroundProcessor(ff, max_queue_size=8192, max_batch_size=128)
processor.submit(Event("model.generation", {"model": "claude"}, conversation_id="conv_123"))

if not processor.shutdown(timeout=10):
    raise RuntimeError(processor.stats().last_error or "Farfield delivery timed out")
```

`submit()` acknowledges queue admission, not durability. `flush()` and
`shutdown()` are explicit delivery boundaries. The processor snapshots caller
context and privacy policy before enqueueing, groups events by conversation,
and exposes committed, dropped, failed, pending, and batch counters.

## Agent frameworks

Optional adapters are included for OpenAI Agents and Claude Agent SDK:

```bash
pip install 'farfield[openai-agents]'
pip install 'farfield[claude-agent-sdk]'
```

Frameworks that emit OTel GenAI or OpenInference traces send directly to
Farfield's OTLP endpoint. See the complete [integration
guide](../../docs/integrations.md).

## Redact before data leaves the process

```python
from farfield import Event, Farfield


def before_send(event: Event) -> Event | None:
    if event.kind == "debug.internal":
        return None
    return event.with_updates(content="[redacted]")


ff = Farfield(before_send=before_send)
```

## Explore

```python
records = ff.query(agent="support-agent", kind="tool.result", limit=50)
conversations = ff.conversations(limit=20)
```

The core package has no runtime dependencies and supports Python 3.10+.
Framework packages are only installed through their named extras.
