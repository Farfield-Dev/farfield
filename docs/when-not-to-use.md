# When not to use Farfield yet

Do not use the current pre-release as:

- A production durable-execution engine. The run journal and its recovery
  semantics work, but worker coordination, leases, signals, timers, and
  automatic resumption are not implemented.
- An internet-facing multi-tenant service. Authentication, authorization, quotas, and tenant isolation are not implemented.
- A high-volume query backend. Queries currently scan immutable records and
  exact conversation segment prefix and do not yet use manifests or a rebuildable
  projection.
- A compliance control by itself. PII redaction, retention enforcement, legal hold, and audit administration are roadmap items.
- A replacement for general infrastructure telemetry. Farfield is being designed around agent conversations and execution; use mature metrics, logs, and trace systems for ordinary services.

It is appropriate today for evaluating the data model, capturing development
histories, testing immutable S3 semantics, journaling run transitions and
checkpoints, building SDKs, and contributing to the worker protocol.
