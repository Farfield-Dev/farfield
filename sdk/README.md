# SDKs

Farfield's core is Go; its SDKs are native to the applications they instrument and execute.

Planned first-class packages:

- `sdk/python`: OpenAI Agents, LangChain/LangGraph, Python workers
- `sdk/typescript`: Vercel AI SDK, OpenAI Agents JS, Node workers
- `sdk/go`: Go workers and direct clients

SDKs will share protocol fixtures rather than private implementation code.
