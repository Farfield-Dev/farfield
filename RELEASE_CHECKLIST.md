# Release checklist

## Code and protocol

- [ ] `make check` passes from a clean checkout.
- [ ] The History and Runtime CLI lifecycle, verification, server, and inspector flow works.
- [ ] S3 conformance passes against MinIO in CI.
- [ ] Golden protocol fixtures pass and any persisted-byte change has migration notes.
- [ ] `govulncheck ./...` reports no reachable known vulnerabilities.
- [ ] Linux, macOS, and Windows release binaries build with `CGO_ENABLED=0`.

## Documentation

- [ ] README quickstart works exactly as written.
- [ ] `openapi.yaml` matches implemented routes and fields.
- [ ] Current boundaries and `docs/when-not-to-use.md` remain accurate.
- [ ] Changelog and version strings agree with the release tag.
- [ ] No roadmap feature is presented as currently available.

## Repository

- [ ] License, security policy, code of conduct, governance, issue forms, and pull request template are visible.
- [ ] Branch protection requires CI and S3 conformance.
- [ ] Repository description, topics, website, and social preview are configured.
- [ ] Release binaries and checksums are attached to the GitHub release.
- [ ] A fresh user can clone, run the example, and open the inspector without an account.
