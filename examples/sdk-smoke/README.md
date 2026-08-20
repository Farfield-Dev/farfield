# Native SDK smoke test

These small programs write through the public Go and TypeScript SDKs to a
running Farfield server, then read the conversation back. They are useful for
checking an installed server against its real object-store backend rather than
an in-memory test double.

```bash
export FARFIELD_ENDPOINT=http://127.0.0.1:8787
export FARFIELD_CONVERSATION=conv_sdk_smoke

go run ./examples/sdk-smoke/go

npm ci --prefix sdk/typescript
npm run build --prefix sdk/typescript
node examples/sdk-smoke/typescript.mjs
```

Each execution creates a new immutable record, so the smoke test can be rerun
without reusing an idempotency key.
