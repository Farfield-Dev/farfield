# Contributing

Farfield is pre-1.0, but persisted bytes are already treated carefully. A change to an object key, checksum input, schema version, or idempotency rule must include compatibility fixtures and migration notes.

## Development

```bash
make check
go build ./cmd/farfield
```

Keep packages narrow and dependency direction explicit:

- Domain packages may depend on `storage` and private helpers.
- `storage` must not depend on History or Runtime.
- History must not require Runtime.
- SDK-specific types must not enter persisted protocols.
- Shared code belongs in a named domain package or `internal/`, not a generic `utils` package.

New behavior should include table-driven tests. Storage implementations must pass the shared conformance suite before being described as supported.
