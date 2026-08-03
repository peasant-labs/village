# Contributing

Thanks for helping improve Village.

1. Search existing issues before opening a new one. For a bug, include a minimal
   reproduction, expected and actual behavior, and relevant logs with secrets and
   transcript content removed.
2. Discuss substantial behavior, schema, or API changes in an issue before
   implementation. Security reports must follow [`SECURITY.md`](SECURITY.md), not
   a public issue.
3. Keep changes focused and add tests that exercise the production path. Follow
   the repository rules in [`AGENTS.md`](AGENTS.md) and the detailed patterns in
   [`TESTING.md`](TESTING.md).
4. Run the relevant build, race-enabled tests, formatting, and vet checks before
   submitting a pull request. Explain any integration check you could not run.
5. Update documentation with behavior, contract, migration, or operational
   changes. Database invariant changes must update
   [`docs/database-invariants.md`](docs/database-invariants.md) in the same pull
   request.

By participating, you agree to follow the
[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md).
