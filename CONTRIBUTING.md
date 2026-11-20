# Contributing to OpenKSM

Thank you for your interest in contributing to **OpenKSM**! We welcome bug reports, documentation improvements, security fixes, and feature contributions.

## Code of Conduct

All contributors are expected to follow our [Code of Conduct](CODE_OF_CONDUCT.md).

## How to Contribute

1. **Fork & Branch:** Fork the repository and create a descriptive branch from `main` (e.g. `feat/collection-offline-sync`, `fix/invoice-idempotency`).
2. **Keep Changes Focused:** Submit one logical change per pull request.
3. **Follow Security & Privacy Rules:**
   - Never commit secrets, API keys, certificates, or `.env` files.
   - Never log personal identifiable information (PII) or credentials.
   - Preserve server-side authorization checks and multi-organization isolation boundaries.
4. **Testing & Quality:**
   - Include unit/integration tests for new behavior, including negative authorization tests where applicable.
   - Ensure linters, type checks, and tests pass before opening a pull request.
5. **Documentation:** Update relevant documentation if your change affects configuration, APIs, or deployment.

## Reporting Bugs & Suggesting Features

- Use the GitHub issue templates for bug reports and feature requests.
- For **security vulnerabilities**, please see [SECURITY.md](SECURITY.md) instead of opening a public issue.

## License

By contributing to OpenKSM, you agree that your contributions will be licensed under the [Apache License, Version 2.0](LICENSE).
