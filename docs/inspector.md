# Inspector

Farfield's embedded inspector is a local, read-only view over captured History.
It does not become a second source of truth: conversation summaries are read
from the rebuildable projection, and the selected session is hydrated from its
immutable records.

Start the server and open `http://127.0.0.1:8787`:

```bash
go run ./cmd/farfield serve
```

Add `?demo=1` to load a dense, deterministic two-week dataset without changing
the configured store. The demo includes explicit reasoning records, a recovered
tool failure, and a terminally failed session so every state can be evaluated.

## Find a session

The activity overview reports agents, sessions, and events per day. Use it to
understand capture volume before selecting a session from the compact list.
Session rows show the primary agent, recency, prompt-derived identifier, event
count, duration, and additional-agent count.

The session header then summarizes the captured prompt, agents, model, duration,
turns, model calls, tool calls, and reported token usage. Input, output,
reasoning, and cache-read tokens remain separate when the provider reports them.
Missing usage is displayed as missing; the inspector does not estimate it.

## Review the conversation

Review is the default, high-level view. User prompts and agent responses use
different visual treatments, while intervening model, reasoning, tool, test,
and lifecycle records are grouped into compact activity blocks.

- Expand an activity block to inspect its exact captured records.
- Tool failures are highlighted without hiding later recovery evidence.
- Use **View record** or select an expanded record to open that exact evidence
  in Trace.
- **Latest response** jumps to the final captured agent message.

Review renders captured message content verbatim. It does not summarize,
reinterpret, score, or infer conversation quality.

## Debug the trace

Trace is the forensic view for answering where time went, which tools ran, what
failed, and which immutable record supports a conclusion.

- **Operations** pairs related records such as tool calls and results.
- **Records** exposes every immutable record independently.
- The run map provides a compact position-in-time overview.
- Focus controls isolate tools, explicit reasoning, slow operations, or errors.
- **Pretty** renders common message, reasoning, model, tool, and completion
  payloads; **JSON** exposes the complete selected operation and its records.
- The evidence pane shows timestamps, duration, trace linkage, byte count,
  integrity state, agent attribution, and tags.

The filter input accepts plain text and structured clauses. Clauses combine
with AND; prefix a clause with `-` to negate it. Quoted values preserve spaces,
and `*` enables wildcard matching.

```text
kind:tool.result status:failed
tool:exec -status:complete
model:claude* tokens:>10k
offset:>30s size:>=1kb
tag.environment:production has:trace
"checksum mismatch"
```

Supported fields are `kind`, `agent`, `tool`, `model`, `status`, `trace`,
`offset`, `size`, `tokens`, `has`, and `tag.<name>`. Numeric comparisons accept
`>`, `>=`, `<`, `<=`, or `=`. When a query is active, choose **Matches**,
**Context**, or **Full** to control how much surrounding evidence remains
visible.

## Status and reasoning semantics

A failed tool result does not necessarily mean the session failed. When an
`agent.turn.completed` record exists, its terminal status determines the
session status; earlier tool errors remain visible as recovered evidence. If no
terminal completion exists, a failed, errored, or cancelled record marks the
session failed.

Reasoning is shown only when the captured kind or payload explicitly identifies
it as reasoning or thinking. Farfield does not derive hidden chain-of-thought
from ordinary model messages. Reasoning-token totals likewise come only from
reported usage fields.

## Export

**Export** downloads the selected conversation analysis and hydrated timeline
as JSON. The exported payload is a convenience for debugging; the sealed
History records in object storage remain authoritative.
