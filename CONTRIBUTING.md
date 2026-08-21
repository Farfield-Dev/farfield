# Contributing

Farfield is pre-1.0, but persisted bytes are already treated carefully. A
change to an object key, checksum input, schema version, or idempotency rule
must include golden fixtures and an explicit compatibility decision.

## Development

```bash
make check
go build ./cmd/farfield

cd sdk/typescript && npm ci && npm run check
cd ../python && python -m pip install -e '.[dev]'
ruff check . && ruff format --check . && pyright
python -W error::ResourceWarning -m unittest discover -s tests -v
```

Keep packages narrow and dependency direction explicit:

- Domain packages may depend on `storage` and private helpers.
- `storage` must not depend on History.
- SDK-specific types must not enter persisted protocols.
- Shared code belongs in a named domain package or `internal/`, not a generic `utils` package.

New behavior should include table-driven tests. Storage implementations must pass the shared conformance suite before being described as supported.

SDK changes must preserve the cross-language behavioral contract in
[`docs/design/0003-native-sdk-experience.md`](docs/design/0003-native-sdk-experience.md).
