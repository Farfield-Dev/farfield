import { Farfield } from "../../sdk/typescript/dist/index.js";

const conversationId = process.env.FARFIELD_CONVERSATION ?? "conv_sdk_smoke";
const farfield = new Farfield({ defaults: { agent: "typescript-sdk-smoke" } });

const record = await farfield.capture({
  conversationId,
  kind: "test.sdk.typescript",
  status: "completed",
  content: { message: "written through the public TypeScript SDK" },
});
const timeline = await farfield.timeline(conversationId);

console.log(
  `record=${record.id} conversation=${conversationId} timeline_records=${timeline.length}`,
);
