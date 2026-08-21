# Repository Instructions

## JavaScript package management

- Use pnpm 10.24.0 in `sdk/typescript` and `server/ui`.
- Commit each package's `pnpm-lock.yaml`; never generate or commit npm, Yarn, or Bun lockfiles.
- Run scripts with `pnpm` and local binaries with `pnpm exec`; use `pnpm dlx` for one-off packages.
- Use `pnpm install --frozen-lockfile` for reproducible installs and CI.
