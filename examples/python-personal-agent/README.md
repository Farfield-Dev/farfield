# Python personal research agent

This example runs Claude with Anthropic's server-side web search and records the
complete turn in Farfield. It keeps user messages, model requests, search tool
calls/results, cited assistant content, token usage, failures, and a durable run
journal. Farfield credentials and the Anthropic API key are never included in
captured event content.

The implementation follows Anthropic's official [Python SDK](https://platform.claude.com/docs/en/cli-sdks-libraries/overview),
[web search tool](https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-search-tool),
and [current model](https://platform.claude.com/docs/en/about-claude/models/overview)
documentation. The GCS server uses Google's documented [Application Default
Credentials](https://docs.cloud.google.com/docs/authentication) flow.

Start Farfield against local storage:

```bash
go run ../../cmd/farfield serve
```

Or use Google Cloud Storage with Application Default Credentials:

```bash
gcloud auth application-default login
go run ../../cmd/farfield serve \
  --store gs://YOUR_BUCKET/farfield \
  --listen 127.0.0.1:8787
```

Add `ANTHROPIC_API_KEY` to the repository-root `.env`, then run one prompt or
the retained research suite:

```bash
uv sync
uv run python agent.py --prompt "What should I work on today?"
uv run python agent.py --suite
```

Each invocation prints its conversation and run IDs and appends a compact index
to `test-runs.jsonl`. The authoritative trace remains in the configured object
store. Query the latest retained run without copying any data out of Farfield:

```bash
uv run python query_traces.py
uv run python query_traces.py --conversation conv_personal_...
uv run python query_traces.py --run run_agent_...
uv run python query_traces.py --search '"object storage" latency*' --agent personal-researcher
```

You can also browse the same history at <http://127.0.0.1:8787> or use the CLI:

```bash
go run ../../cmd/farfield history conversations --store gs://YOUR_BUCKET/farfield
go run ../../cmd/farfield history search --store gs://YOUR_BUCKET/farfield --text '"object storage" latency*'
go run ../../cmd/farfield history verify --store gs://YOUR_BUCKET/farfield
go run ../../cmd/farfield runtime verify --store gs://YOUR_BUCKET/farfield
```
