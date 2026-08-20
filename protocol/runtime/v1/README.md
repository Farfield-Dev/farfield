# Runtime protocol v1

Runtime v1 is an immutable, hash-chained run journal stored entirely in the
same object store as Farfield History. It does not require a mutable database
head.

Each run begins with `run.created` at sequence zero. Transitions and
checkpoints compete to create the next predictable sequence-numbered object.
Atomic create-if-absent therefore serializes concurrent writers. Every event
contains the previous event checksum, current status, attempt number, and a
stable operation ID for idempotent retry and ambiguous-commit recovery.

The persisted event contract is [`schema.json`](schema.json). Object keys are:

```text
runtime/v1/runs/<run-id-sha256-prefix>/<run-id-sha256>/events/<20-digit-sequence>.json
```

Run IDs are stored in the event body but not exposed in object keys. Checkpoint
JSON is canonicalized and embedded in the event, with a default one MiB limit.
Run state is reconstructed by verifying and reducing the complete event chain.

Runtime v1 currently defines run creation, status transitions, attempts, and
checkpoints. Worker leases, signals, timers, externally visible action
receipts, and automatic scheduling are not part of this first contract.
