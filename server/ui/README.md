# Farfield inspector UI

The inspector is a React and TypeScript application compiled into static assets
that are embedded in the Farfield Go binary. It has no runtime dependency on a
Node.js server.

## Design system

The visual foundation lives in `src/styles.css`: semantic color, typography,
spacing, and elevation tokens are exposed to Tailwind. Reusable components in
`src/design-system` use Class Variance Authority (CVA) for explicit variants.
Product components should consume semantic variants rather than composing raw
color classes independently.

The design principles are:

- evidence before decoration;
- progressive disclosure for large event payloads;
- dense, readable controls for engineering workflows;
- provenance and integrity are visible product concepts;
- every loading, empty, and error state is intentional.

## Development

Run the Go server on port 8787, then:

```sh
pnpm install --frozen-lockfile
pnpm dev
```

The Vite development server proxies `/v1` to Farfield. Before committing UI
changes, run `pnpm check && pnpm build`; the generated `dist` directory is
embedded by `server/ui.go` and checked into the repository for hermetic Go and
release builds.

Add `?demo=1` to the local URL to load the deterministic two-week demo dataset
used for visual QA and README captures. The fixture is frontend-only and is
never used unless that query flag is present.
