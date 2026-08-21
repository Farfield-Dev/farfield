# When not to use Farfield yet

Do not use the current pre-release as:

- A workflow scheduler or durable-execution engine. Farfield records and
  queries agent activity; it does not execute or resume work.
- An internet-facing multi-tenant service. Authentication, authorization, quotas, and tenant isolation are not implemented.
- A horizontally scaled, high-volume search cluster. Ranked and structured
  queries use the embedded disposable index, but replicas do not yet share
  object-backed index packs or generation manifests.
- A compliance control by itself. PII redaction, retention enforcement, legal hold, and audit administration are roadmap items.
- A replacement for general infrastructure telemetry. Farfield is designed
  around agent conversations and semantic traces; use mature metrics, logs,
  and trace systems for ordinary services.

It is appropriate today for durably capturing, searching, filtering, replaying,
and inspecting development agent histories; testing immutable object-storage
semantics; and integrating the native SDKs.
