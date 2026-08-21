# SDKs

Farfield's core is Go; its SDKs are native to the applications they instrument
and execute.

- [`python`](python/README.md): sync and async Python clients
- [`typescript`](typescript/README.md): typed Node.js client
- [`go`](go/README.md): idiomatic Go HTTP client

All three use the versioned HTTP protocol and share the same behavioral
contract: stable IDs, exact-body retries, durable capture, explicit batching,
bounded background processors, conversation context, privacy hooks, typed
errors, and History reads. They do not duplicate the object-storage
implementation.

Python and TypeScript also ship optional direct adapters for OpenAI Agents and
Claude Agent SDK. Frameworks with OpenTelemetry or OpenInference support use the
core OTLP/HTTP endpoint instead of a bespoke wrapper. See the
[integration guide](../docs/integrations.md) and
[SDK design decision](../docs/design/0003-native-sdk-experience.md).
