# When not to use Farfield yet

Do not use the current pre-release as:

- A production durable-execution engine. The run journal and its recovery
  semantics work, but worker coordination, leases, signals, timers, and
  automatic resumption are not implemented.
- An internet-facing multi-tenant service. Authentication, authorization, quotas, and tenant isolation are not implemented.
- A horizontally scaled, high-volume search cluster. Ranked and structured
  queries use the embedded disposable index, but replicas do not yet share
  object-backed index packs or generation manifests.
- A compliance control by itself. PII redaction, retention enforcement, legal hold, and audit administration are roadmap items.
- A replacement for general infrastructure telemetry. Farfield is being designed around agent conversations and execution; use mature metrics, logs, and trace systems for ordinary services.

It is appropriate today for durably capturing, searching, filtering, replaying,
and inspecting development agent histories; testing immutable object-storage
semantics; journaling run transitions and checkpoints; and integrating the
native SDKs.
