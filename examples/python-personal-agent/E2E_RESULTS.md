# Retained end-to-end run

This example was exercised on 2026-08-20 against a real Google Cloud Storage
bucket and live Anthropic API. These are integration artifacts, not synthetic
fixtures. They remain intentionally available in this directory and in the
configured private bucket for inspection.

## Environment

- Store: `gs://farfield-e2e-masonry-so-20260820/agent-e2e`
- Model: `claude-sonnet-4-6`
- Tool: Anthropic server-side web search
- Farfield endpoint during the run: `http://127.0.0.1:8787`
- Authentication: Google Application Default Credentials and a repository-root
  `.env` excluded from Git

## Primary retained conversation

Conversation: `conv_personal_20260820T073714Z_2a252dddf2`

| Prompt | Run | Status | Input tokens |
| --- | --- | --- | ---: |
| Long-horizon agent landscape | `run_agent_20260820T073714Z_1182ddbb23` | completed | 17,684 |
| GCS agent-journal design | `run_agent_20260820T073742Z_60bc57d393` | completed | 17,606 |
| Open-source launch plan | `run_agent_20260820T073814Z_5e5a1d7da7` | completed | 20,533 |

The conversation contains 36 semantic records: three user messages, three model
requests, nine web-search calls, nine web-search results, three normalized
assistant messages, three lossless provider responses, and start/completion
events for each turn. Every run contains a four-event hash chain: creation,
start, response checkpoint, and completion.

`test-runs.jsonl` is the machine-readable run index. The complete readable
answers are retained in `test-suite-optimized-output.md`. An earlier run in
`test-suite-output.md` deliberately remains as evidence of a DX issue found by
the test: feeding lossless encrypted search blocks back into later model turns
caused the third turn to consume 151,183 input tokens. Keeping lossless blocks
in Farfield while carrying only normalized assistant text in model context
reduced the optimized third turn to 20,533 tokens. The suites also used
different search/output limits, so this is an integration observation rather
than a controlled model benchmark.

## Store verification

After the agent suites, CLI idempotency/error cases, and native SDK smoke tests:

```text
History: 164 records · 15 segments · 4 blobs · 0 orphans · 0 issues
Runtime: 8 runs · 32 events · 0 issues
```

The native GCS conformance suite separately passed first-create, same-body
retry, different-body conflict, immediate get/list, missing-object, and eight
concurrent-writer cases. Exactly one concurrent writer created the target.

## Query it

With Application Default Credentials active, start the server:

```bash
go run ./cmd/farfield serve \
  --store gs://farfield-e2e-masonry-so-20260820/agent-e2e \
  --listen 127.0.0.1:8787
```

Then use any supported surface:

```bash
cd examples/python-personal-agent
uv run python query_traces.py --conversation conv_personal_20260820T073714Z_2a252dddf2
uv run python query_traces.py --run run_agent_20260820T073814Z_5e5a1d7da7

go run ./cmd/farfield history timeline \
  --store gs://farfield-e2e-masonry-so-20260820/agent-e2e \
  --conversation conv_personal_20260820T073714Z_2a252dddf2

go run ./cmd/farfield history verify \
  --store gs://farfield-e2e-masonry-so-20260820/agent-e2e
```

The same conversation is visible in the embedded inspector at
<http://127.0.0.1:8787>.

## Issues found and fixed by the live run

- `inspect.py` shadowed Python's standard-library `inspect`; the helper is now
  named `query_traces.py`.
- Timeline hydration fetched the same segment once per inline record. Reusing
  verified segment objects reduced the 106-record GCS timeline from 20.9
  seconds to 1.45 seconds while returning byte-identical JSON.
- Raw tool payloads overwhelmed the inspector. Large events now show a useful
  summary and an explicit expandable full payload.
- A slower default timeline request could overwrite a conversation selected by
  the developer. The inspector now ignores stale responses.
- `.env` was not ignored. Secret-bearing environment files and virtual
  environments are now excluded while `.env.example` remains trackable.
- Replaying server-search blocks made model context grow rapidly. Farfield now
  retains the lossless response while the agent passes normalized text between
  turns.
