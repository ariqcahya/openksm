# OpenKSM

**OpenKSM** is an open-source, self-hosted community waste management platform (*Kelompok Swadaya Masyarakat* / TPS3R / TPST / Waste Bank / Local Operator).

> **Project Status:** Early-stage / Active Initial Development. Application source code and deployment bundles are currently being built.

---

## Key Principles

- **Self-Hosted & Sovereign:** Run your own instance using Docker Compose without depending on a mandatory central SaaS.
- **Region-Agnostic & Multi-Organization:** Designed for any community or regional waste operator—one instance can manage one or multiple organizations with strict data isolation.
- **Workflow-Driven Operations:** Structured task-based communication (collections, billing, payments, complaints, facility intake) rather than unstructured chat.
- **Security & Auditability First:** Server-enforced authorization (RBAC + ABAC), separation of duties (maker-checker) for financial adjustments, and append-only audit trails.
- **Offline-Tolerant Field Work:** Built for mobile/low-bandwidth usage (360px+ responsive PWA) with conflict-aware synchronization.

## Planned Architecture

- **Backend:** Go modular monolith (`chi`, `pgx`, `sqlc`, OpenAPI 3.1) + PostgreSQL-backed transactional outbox worker
- **Frontend:** React + TypeScript + Vite (PWA)
- **Data Infrastructure:** PostgreSQL (database-per-organization isolation), private object storage, and containerized deployment

## Getting Started (Self-Hosted Docker)

> **Prerequisites:** Linux / macOS with Docker Engine & Docker Compose plugin, `openssl`.

1. **Generate `.env` with secure random secrets (`chmod 600`):**
   ```bash
   ./scripts/openksm.sh setup
   ```
2. **Verify host prerequisites and Compose configuration:**
   ```bash
   ./scripts/openksm.sh doctor
   ```
3. **Build and start the OpenKSM + IDNBase stack:**
   ```bash
   ./scripts/openksm.sh up
   ```
4. **Operations & Recovery:**
   - Check status: `./scripts/openksm.sh status`
   - Create checksummed backup: `./scripts/openksm.sh backup`
   - Run isolated restore drill: `./scripts/openksm.sh restore-drill`
   - Check IDNBase upgrade compatibility: `./scripts/openksm.sh upgrade-check 1.2.1`

## Compatibility Matrix

| OpenKSM Version | Verified IDNBase Version | Notes |
|---|---|---|
| `0.7.0-dev` | `1.2.1` | Digest-pinned unmodified official IDNBase images (`deploy/manifest.lock.yaml`) |

## Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) and our [Code of Conduct](CODE_OF_CONDUCT.md) before opening a pull request.

## Security

If you discover a security vulnerability, **do not** open a public GitHub issue. Please follow the responsible disclosure guidelines in [SECURITY.md](SECURITY.md).

## License

Licensed under the [Apache License, Version 2.0](LICENSE).
