# Farfield History segmented record protocol

History v2 stores one or more record envelopes and their canonical JSON content
inside an immutable segment. A successful segment write is the durable commit
point for every entry it contains.

Segments contain records from exactly one conversation and are placed under a
deterministic conversation shard. Segment IDs provide batch-level idempotency:
retrying the same ID with equivalent logical records returns the committed
segment, while reusing it for different data is a conflict.

Record v1 remains readable. It stores content-addressed payload and record
objects separately. Record v2 uses a segment locator consisting of the segment
object key and entry index. Readers verify the segment checksum, record
checksum, content checksum, key, and index before returning content.

The normative JSON shape is [schema.json](schema.json).
