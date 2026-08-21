# Farfield History segmented record protocol

History v2 stores one or more record envelopes and their canonical JSON content
inside an immutable segment. A successful segment write is the durable commit
point for every entry it contains.

Segments contain records from exactly one conversation and are placed under the
full deterministic conversation hash. Segment IDs provide batch-level idempotency:
retrying the same ID with equivalent logical records returns the committed
segment, while reusing it for different data is a conflict.

All records use a segment locator consisting of the conversation-local segment
object key and entry index. A normal one-record append is represented by a
one-entry segment, so small canonical JSON remains inline. Readers verify the
segment checksum, record checksum, content checksum, key, and index before
returning content. Content above the inline threshold uses an immutable
content-addressed blob.

Object keys are query-aligned:

```text
history/v2/conversations/<conversation-sha256>/segments/<segment-sha256>.json
history/v2/blobs/sha256/<first-two-content-sha256>/<remaining-content-sha256>
```

The normative JSON shape is [schema.json](schema.json).
