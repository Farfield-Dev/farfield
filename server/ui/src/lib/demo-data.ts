import type { ConversationSummary, HistoryRecord, JSONValue, TimelineEntry } from "./history";

type Scenario = {
  slug: string;
  agents: string[];
  prompt: string;
  daysAgo: number;
  hour: number;
  records: number;
  durationMinutes: number;
  model: string;
  tools: string[];
  environment: "production" | "staging" | "development";
  team: string;
  failure?: {
    toolCall: number | "last";
    terminal: boolean;
    code: string;
    message: string;
  };
};

const scenarios: Scenario[] = [
  { slug: "release-readiness-audit", agents: ["release-engineer", "code-reviewer"], prompt: "Audit the release candidate, verify migrations and rollback safety, then produce a launch recommendation with blocking issues separated from follow-ups.", daysAgo: 0, hour: 16, records: 58, durationMinutes: 7.8, model: "claude-sonnet-4-6", tools: ["read_file", "exec", "github_search"], environment: "production", team: "platform", failure: { toolCall: 2, terminal: false, code: "COMMAND_EXIT_1", message: "Migration dry-run detected a checksum mismatch in 20260818_add_run_index.sql" } },
  { slug: "checkout-incident-triage", agents: ["incident-commander", "log-analyst"], prompt: "Investigate the checkout latency regression, correlate deployment and trace evidence, and propose the safest mitigation with explicit confidence levels.", daysAgo: 0, hour: 13, records: 46, durationMinutes: 5.2, model: "gpt-5.4", tools: ["query_logs", "fetch_metrics", "deployment_diff"], environment: "production", team: "reliability", failure: { toolCall: "last", terminal: true, code: "UPSTREAM_TIMEOUT", message: "Log search backend did not respond within 30 seconds after three attempts" } },
  { slug: "enterprise-renewal-brief", agents: ["account-researcher"], prompt: "Prepare a concise renewal brief using recent product usage, support themes, and open commitments. Flag every inference that needs account-team confirmation.", daysAgo: 0, hour: 9, records: 31, durationMinutes: 3.4, model: "gemini-2.5-pro", tools: ["warehouse_query", "search_tickets", "read_crm"], environment: "production", team: "growth" },
  { slug: "sdk-breaking-change-review", agents: ["code-reviewer"], prompt: "Review the TypeScript SDK diff for accidental breaking changes, unsafe retries, and protocol incompatibilities before merge.", daysAgo: 1, hour: 15, records: 42, durationMinutes: 4.6, model: "claude-sonnet-4-6", tools: ["read_file", "exec", "github_search"], environment: "staging", team: "developer-experience" },
  { slug: "support-escalation-4821", agents: ["support-agent", "incident-commander"], prompt: "Resolve escalation 4821 by reconstructing the failed workflow, identifying the customer-visible impact, and drafting a technically precise response.", daysAgo: 1, hour: 10, records: 37, durationMinutes: 3.9, model: "gpt-5.4", tools: ["search_tickets", "query_logs", "read_crm"], environment: "production", team: "support" },
  { slug: "object-store-cost-review", agents: ["finops-analyst"], prompt: "Analyze object storage request and retention costs for the last 30 days and identify optimizations that preserve replay and audit guarantees.", daysAgo: 2, hour: 17, records: 35, durationMinutes: 4.1, model: "gemini-2.5-pro", tools: ["warehouse_query", "fetch_metrics", "calculator"], environment: "production", team: "infrastructure" },
  { slug: "auth-threat-model", agents: ["security-reviewer", "code-reviewer"], prompt: "Threat-model the new service-token flow, focusing on confused deputy risks, replay resistance, tenant boundaries, and credential rotation.", daysAgo: 2, hour: 12, records: 49, durationMinutes: 6.3, model: "claude-sonnet-4-6", tools: ["read_file", "github_search", "search_advisories"], environment: "staging", team: "security" },
  { slug: "onboarding-dropoff-analysis", agents: ["product-analyst"], prompt: "Find the largest onboarding drop-off, validate the event instrumentation, and recommend one experiment with a measurable success criterion.", daysAgo: 2, hour: 8, records: 28, durationMinutes: 2.7, model: "gpt-5.4-mini", tools: ["warehouse_query", "fetch_metrics", "experiment_catalog"], environment: "production", team: "product" },
  { slug: "regional-failover-drill", agents: ["incident-commander"], prompt: "Evaluate the regional failover drill against recovery objectives and list gaps in automation, evidence capture, and operator guidance.", daysAgo: 3, hour: 14, records: 44, durationMinutes: 5.8, model: "gpt-5.4", tools: ["query_logs", "fetch_metrics", "read_runbook"], environment: "production", team: "reliability" },
  { slug: "docs-accuracy-sweep", agents: ["documentation-agent", "code-reviewer"], prompt: "Verify the quickstart against the current CLI and SDK behavior, then identify stale examples and missing failure-mode guidance.", daysAgo: 4, hour: 16, records: 34, durationMinutes: 3.3, model: "claude-sonnet-4-6", tools: ["read_file", "exec", "link_check"], environment: "staging", team: "developer-experience" },
  { slug: "refund-policy-evaluation", agents: ["support-agent"], prompt: "Evaluate the proposed refund-policy assistant against edge cases from recent tickets and summarize where human approval must remain mandatory.", daysAgo: 4, hour: 9, records: 26, durationMinutes: 2.5, model: "gpt-5.4-mini", tools: ["search_tickets", "read_policy", "eval_runner"], environment: "development", team: "support" },
  { slug: "queue-saturation-analysis", agents: ["log-analyst"], prompt: "Explain the queue saturation between 14:00 and 15:00 UTC using traces and worker metrics, and distinguish root cause from downstream symptoms.", daysAgo: 5, hour: 15, records: 41, durationMinutes: 4.9, model: "gemini-2.5-pro", tools: ["query_logs", "fetch_metrics", "trace_search"], environment: "production", team: "reliability" },
  { slug: "dependency-upgrade-plan", agents: ["code-reviewer"], prompt: "Plan the dependency upgrade in low-risk batches, identify transitive incompatibilities, and define verification gates for each batch.", daysAgo: 6, hour: 13, records: 32, durationMinutes: 3.1, model: "claude-sonnet-4-6", tools: ["read_file", "exec", "search_advisories"], environment: "development", team: "platform" },
  { slug: "customer-health-backfill", agents: ["product-analyst", "account-researcher"], prompt: "Validate the customer-health backfill, reconcile row counts with the source systems, and isolate accounts whose scores changed materially.", daysAgo: 6, hour: 8, records: 39, durationMinutes: 4.4, model: "gpt-5.4", tools: ["warehouse_query", "read_crm", "data_diff"], environment: "production", team: "growth" },
  { slug: "api-latency-budget", agents: ["finops-analyst", "log-analyst"], prompt: "Build an evidence-backed latency budget for the ingestion API and identify which storage operations dominate the tail.", daysAgo: 7, hour: 17, records: 36, durationMinutes: 3.8, model: "gemini-2.5-pro", tools: ["fetch_metrics", "trace_search", "calculator"], environment: "production", team: "infrastructure" },
  { slug: "secrets-scanner-evaluation", agents: ["security-reviewer"], prompt: "Evaluate the new secrets scanner against representative fixtures, false-positive cases, and encoded credential formats.", daysAgo: 7, hour: 11, records: 29, durationMinutes: 2.9, model: "gpt-5.4-mini", tools: ["read_file", "exec", "eval_runner"], environment: "staging", team: "security" },
  { slug: "cli-usability-review", agents: ["documentation-agent"], prompt: "Run the CLI from a clean environment, record confusing states and recovery paths, and propose precise copy changes.", daysAgo: 8, hour: 14, records: 24, durationMinutes: 2.2, model: "claude-sonnet-4-6", tools: ["exec", "read_file", "capture_output"], environment: "development", team: "developer-experience" },
  { slug: "retention-policy-migration", agents: ["release-engineer"], prompt: "Validate the retention-policy migration plan against existing buckets, legal holds, and rollback requirements.", daysAgo: 9, hour: 16, records: 38, durationMinutes: 4.7, model: "gpt-5.4", tools: ["warehouse_query", "read_policy", "bucket_inventory"], environment: "production", team: "platform" },
  { slug: "weekly-incident-digest", agents: ["incident-commander", "documentation-agent"], prompt: "Produce the weekly incident digest with verified impact, contributing factors, and overdue corrective actions.", daysAgo: 9, hour: 9, records: 33, durationMinutes: 3.6, model: "gemini-2.5-pro", tools: ["search_tickets", "query_logs", "read_runbook"], environment: "production", team: "reliability" },
  { slug: "search-relevance-eval", agents: ["product-analyst"], prompt: "Compare search relevance across the current and candidate rankers, segment failures by query class, and recommend a ship decision.", daysAgo: 10, hour: 13, records: 43, durationMinutes: 5.1, model: "claude-sonnet-4-6", tools: ["warehouse_query", "eval_runner", "experiment_catalog"], environment: "staging", team: "product" },
  { slug: "vendor-security-review", agents: ["security-reviewer", "account-researcher"], prompt: "Review the vendor security package, map evidence to our control requirements, and flag unanswered questions before approval.", daysAgo: 11, hour: 15, records: 30, durationMinutes: 3.2, model: "gpt-5.4", tools: ["read_file", "search_advisories", "read_crm"], environment: "production", team: "security" },
  { slug: "archive-replay-benchmark", agents: ["release-engineer", "finops-analyst"], prompt: "Benchmark replay from archived history across representative run sizes and report throughput, tail latency, and request cost.", daysAgo: 12, hour: 12, records: 47, durationMinutes: 6.1, model: "gemini-2.5-pro", tools: ["exec", "fetch_metrics", "calculator"], environment: "staging", team: "infrastructure" },
  { slug: "launch-faq-draft", agents: ["documentation-agent"], prompt: "Draft a technically accurate launch FAQ from the current product boundaries, emphasizing durability guarantees and explicit non-goals.", daysAgo: 13, hour: 10, records: 27, durationMinutes: 2.8, model: "claude-sonnet-4-6", tools: ["read_file", "github_search", "link_check"], environment: "development", team: "developer-experience" },
];

function startedAt(scenario: Scenario) {
  const date = new Date();
  date.setHours(scenario.hour, 12, 0, 0);
  date.setDate(date.getDate() - scenario.daysAgo);
  return date;
}

function providerForModel(model: string) {
  if (model.startsWith("claude")) return "anthropic";
  if (model.startsWith("gemini")) return "google";
  return "openai";
}

function toolInput(tool: string, scenario: Scenario, index: number): JSONValue {
  if (["web_search", "github_search", "search_advisories", "search_tickets"].includes(tool)) return { query: `${scenario.slug.replaceAll("-", " ")} evidence ${index + 1}` };
  if (tool === "exec") return { command: index % 2 ? "go test ./..." : "git diff --check" };
  if (["read_file", "read_policy", "read_runbook"].includes(tool)) return { path: `docs/${scenario.slug}.md`, line_start: 1, line_end: 160 };
  return { scope: scenario.team, window: "24h", operation: tool.replaceAll("_", " ") };
}

function toolResult(tool: string, scenario: Scenario, index: number, failed: boolean): JSONValue {
  if (failed && scenario.failure) {
    return {
      ok: false,
      error: {
        code: scenario.failure.code,
        message: scenario.failure.message,
        retryable: !scenario.failure.terminal,
      },
      attempt: scenario.failure.terminal ? 3 : 1,
      tool,
    };
  }
  if (["web_search", "github_search", "search_advisories", "search_tickets"].includes(tool)) {
    return {
      content: Array.from({ length: 3 }, (_, result) => ({
        title: `${scenario.team} evidence ${index + 1}.${result + 1}`,
        url: `https://example.com/${scenario.slug}/${index + 1}/${result + 1}`,
        page_age: result === 0 ? "2 hours" : `${result + 1} days`,
      })),
    };
  }
  return { ok: true, rows: 120 + index * 7, summary: `${tool.replaceAll("_", " ")} completed with verified output` };
}

function contentForKind(kind: string, scenario: Scenario, index: number, tool: string, failed = false): JSONValue {
  if (kind === "agent.turn.started") return { prompt_name: scenario.slug, objective: scenario.prompt };
  if (kind === "message.user") return { text: scenario.prompt };
  if (kind === "model.request") return { model: scenario.model, provider: providerForModel(scenario.model), max_tokens: 4096, message_count: 4 + index, tools: scenario.tools };
  if (kind === "model.reasoning") return { type: "reasoning", text: `Checking the captured evidence and constraints for ${scenario.slug.replaceAll("-", " ")} before the next action.`, token_count: 320 + index * 7 };
  if (kind === "model.response") return { model: scenario.model, provider: providerForModel(scenario.model), stop_reason: "tool_use" };
  if (kind === "tool.call") return { name: tool, input: toolInput(tool, scenario, index) };
  if (kind === "tool.result") return toolResult(tool, scenario, index, failed);
  if (kind === "test.evidence") return { check: `evidence-${index + 1}`, passed: true, confidence: 0.94 };
  if (kind === "message.assistant") return { text: `Checkpoint ${index + 1}: evidence reconciled for **${scenario.slug.replaceAll("-", " ")}**. Continuing with the remaining verification steps.` };
  if (kind === "agent.turn.completed") return {
    stop_reason: scenario.failure?.terminal ? "error" : "end_turn",
    ...(scenario.failure?.terminal ? { error: { code: scenario.failure.code, message: scenario.failure.message } } : {}),
    usage: { input_tokens: 18_400 + scenario.records * 211, output_tokens: 2_100 + scenario.records * 43, output_tokens_details: { reasoning_tokens: 720 + scenario.records * 9 }, cache_read_input_tokens: 6_200, service_tier: "standard" },
  };
  return { name: kind, index };
}

function buildTimeline(scenario: Scenario): TimelineEntry[] {
  const first = startedAt(scenario).getTime();
  const duration = scenario.durationMinutes * 60_000;
  const middleKinds = ["model.reasoning", "tool.call", "tool.result", "model.response", "message.assistant", "tool.call", "tool.result", "test.evidence"];
  const kinds = ["agent.turn.started", "message.user", "model.request"];
  while (kinds.length < scenario.records - 2) kinds.push(middleKinds[(kinds.length - 3) % middleKinds.length]);
  kinds.push("message.assistant", "agent.turn.completed");

  let activeTool = scenario.tools[0];
  let toolCallCount = 0;
  const totalToolCalls = kinds.filter((kind) => kind === "tool.call").length;
  const failureToolCall = scenario.failure?.toolCall === "last" ? totalToolCalls : scenario.failure?.toolCall;
  let activeToolCall = 0;
  let pendingFailureNarration = false;
  let failedTool: string | null = null;

  return kinds.map((kind, index) => {
    const occurred = new Date(first + (duration * index) / Math.max(1, kinds.length - 1)).toISOString();
    if (kind === "tool.call") {
      activeTool = scenario.tools[toolCallCount % scenario.tools.length];
      toolCallCount += 1;
      activeToolCall = toolCallCount;
    }
    const tool = activeTool;
    const toolFailed = kind === "tool.result" && activeToolCall === failureToolCall;
    const narratesFailure = kind === "message.assistant" && pendingFailureNarration;
    if (toolFailed) {
      pendingFailureNarration = true;
      failedTool = tool;
    }
    const content = index === kinds.length - 2
      ? scenario.failure?.terminal
        ? { text: `## Unable to complete\n\nThe **${scenario.slug.replaceAll("-", " ")}** run stopped because \`${scenario.failure.code}\` prevented the final evidence check.\n\n- ${scenario.failure.message}\n- No mitigation recommendation was issued\n- Partial evidence remains available for debugging` }
        : { text: `## Recommendation\n\nThe **${scenario.slug.replaceAll("-", " ")}** run completed with ${scenario.tools.length} evidence sources reconciled.\n\n- Primary checks passed\n- One recoverable tool failure was retried successfully\n- Remaining risk is bounded and documented\n- All supporting records are sealed in immutable history` }
      : narratesFailure && scenario.failure
        ? { text: `The **${(failedTool ?? tool).replaceAll("_", " ")}** call failed with \`${scenario.failure.code}\`. I switched to the captured fallback evidence path and can continue.` }
        : contentForKind(kind, scenario, index, tool, toolFailed);
    if (narratesFailure) {
      pendingFailureNarration = false;
      failedTool = null;
    }
    const serialized = JSON.stringify(content);
    const record: HistoryRecord = {
      schema_version: "2",
      id: `rec_demo_${scenario.slug}_${String(index + 1).padStart(3, "0")}`,
      conversation_id: `conv_demo_${scenario.slug}`,
      kind,
      occurred_at: occurred,
      recorded_at: new Date(new Date(occurred).getTime() + 14).toISOString(),
      sequence: index + 1,
      trace_id: `trace_demo_${scenario.slug}`,
      span_id: `span_${String(index + 1).padStart(3, "0")}`,
      parent_id: index > 0 ? `span_${String(index).padStart(3, "0")}` : null,
      agent: scenario.agents[index % scenario.agents.length],
      tool: kind.startsWith("tool.") ? tool : null,
      status: toolFailed || (kind === "agent.turn.completed" && scenario.failure?.terminal)
        ? "failed"
        : kind === "agent.turn.started"
          ? "running"
          : kind === "agent.turn.completed" || kind === "tool.result"
            ? "complete"
            : null,
      tags: { environment: scenario.environment, team: scenario.team, prompt: scenario.slug, demo: "true" },
      content: {
        sha256: String(index + 1).padStart(64, "0"),
        size: new TextEncoder().encode(serialized).length,
        media_type: "application/json",
        key: `demo/${scenario.slug}/${String(index + 1).padStart(3, "0")}.json`,
        storage: serialized.length > 700 ? "blob" : "segment",
      },
      record_sha256: String(index + 101).padStart(64, "a"),
    };
    return { record, content };
  });
}

const timelines = new Map<string, TimelineEntry[]>();

export const demoConversations: ConversationSummary[] = scenarios.map((scenario) => {
  const timeline = buildTimeline(scenario);
  const id = `conv_demo_${scenario.slug}`;
  timelines.set(id, timeline);
  return {
    id,
    record_count: timeline.length,
    first_seen_at: timeline[0].record.occurred_at,
    last_seen_at: timeline.at(-1)?.record.occurred_at ?? timeline[0].record.occurred_at,
    agents: scenario.agents,
    kinds: [...new Set(timeline.map((entry) => entry.record.kind))],
  };
}).sort((left, right) => new Date(right.last_seen_at).getTime() - new Date(left.last_seen_at).getTime());

export function demoTimeline(conversationID: string) {
  return structuredClone(timelines.get(conversationID) ?? []);
}

export function isDemoMode() {
  return new URLSearchParams(window.location.search).get("demo") === "1";
}
