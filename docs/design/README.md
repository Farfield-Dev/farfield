# Design documents

Design documents record substantial changes to Farfield's storage model,
protocols, and execution semantics. They explain why a design exists, the
constraints it must satisfy, its tradeoffs, and the prior art that informed it.

The possible statuses are:

- **Proposed**: direction under discussion; not a product guarantee.
- **Accepted**: approved for implementation.
- **Implemented**: reflected in released code and normative documentation.
- **Superseded**: retained as historical context and linked to its replacement.

Implementation and user-facing guarantees live in the relevant package,
protocol, and operational documentation. A design document does not make an
unimplemented capability available.

| Document | Status |
| --- | --- |
| [0001: Object-storage-native history engine](0001-object-storage-native-history.md) | Proposed |
| [0002: Agent workload and object-storage fit](0002-agent-workload-storage-fit.md) | Proposed |
| [0003: Native SDK experience](0003-native-sdk-experience.md) | Accepted |
| [0004: Object-storage-native conversation projection](0004-object-storage-conversation-projection.md) | Implemented |
| [0005: Query-aligned conversation segments](0005-query-aligned-conversation-segments.md) | Implemented |
| [0006: Disposable indexed search](0006-disposable-indexed-search.md) | Implemented |
| [0007: Protocol-first agent framework integrations](0007-protocol-first-integrations.md) | Implemented |
