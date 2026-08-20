# SDKs

Farfield's core is Go; its SDKs are native to the applications they instrument
and execute.

- [`python`](python/README.md): sync and async Python clients
- [`typescript`](typescript/README.md): typed Node.js client
- [`go`](go/README.md): idiomatic Go HTTP client

All three use the versioned HTTP protocol and share the same behavioral
contract: stable IDs, exact-body retries, durable capture, explicit batching,
conversation context, privacy hooks, typed errors, and Runtime journal access.
They do not duplicate the object-storage implementation.

Framework adapters remain separate packages layered on these clients. See the
[SDK design decision](../docs/design/0003-native-sdk-experience.md).
