#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="${ROOT_DIR}/deploy/compose.yaml"
ENV_FILE="${ROOT_DIR}/.env"
ENV_EXAMPLE="${ROOT_DIR}/.env.example"

usage() {
  cat <<EOF
OpenKSM Host CLI (DEVOPS-002)

Usage:
  $(basename "$0") setup [--force]   Generate .env with random secrets (chmod 600)
  $(basename "$0") doctor            Check Docker, Compose, .env, permissions, and config
  $(basename "$0") up                Build (if needed) and start OpenKSM + IDNBase stack
  $(basename "$0") down              Stop stack (preserves persistent volumes)
  $(basename "$0") status            Show container status
  $(basename "$0") backup            Create verified PostgreSQL backup with SHA256 checksums
  $(basename "$0") restore-drill     Test restoring latest backup into isolated drill DB (measures RTO)
  $(basename "$0") upgrade-check <v> Check manifest.lock.yaml compatibility before upgrading IDNBase
EOF
}

gen_hex() {
  local bytes="$1"
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex "${bytes}"
  else
    head -c "${bytes}" /dev/urandom | od -An -tx1 | tr -d ' \n'
  fi
}

cmd_setup() {
  local force="${1:-}"
  if [ -f "${ENV_FILE}" ] && [ "${force}" != "--force" ]; then
    echo "[setup] ${ENV_FILE} already exists. Keeping existing secrets (use --force to regenerate)."
    chmod 600 "${ENV_FILE}"
    return 0
  fi

  if [ ! -f "${ENV_EXAMPLE}" ]; then
    echo "Error: ${ENV_EXAMPLE} not found." >&2
    exit 1
  fi

  local pg_pass reg_pass master_key jwt_secret
  pg_pass="$(gen_hex 24)"
  reg_pass="$(gen_hex 24)"
  master_key="$(gen_hex 32)"
  jwt_secret="$(gen_hex 32)"

  sed \
    -e "s|^POSTGRES_BOOTSTRAP_PASSWORD=.*|POSTGRES_BOOTSTRAP_PASSWORD=${pg_pass}|" \
    -e "s|^OPENKSM_REGISTRY_DB_PASSWORD=.*|OPENKSM_REGISTRY_DB_PASSWORD=${reg_pass}|" \
    -e "s|^MASTER_KEY_HEX=.*|MASTER_KEY_HEX=${master_key}|" \
    -e "s|^GRAPHQL_JWT_SECRET=.*|GRAPHQL_JWT_SECRET=${jwt_secret}|" \
    "${ENV_EXAMPLE}" > "${ENV_FILE}"

  chmod 600 "${ENV_FILE}"
  echo "[setup] Created ${ENV_FILE} with generated secrets and mode 600."
  echo "[setup] Persistent volumes: secrets_idnbase, pgdata_idnbase, backups_idnbase, storage_idnbase, caddy_data, caddy_config"
}

cmd_doctor() {
  local failed=0
  echo "[doctor] Checking host prerequisites..."

  if command -v docker >/dev/null 2>&1; then
    echo "  OK: $(docker --version)"
  else
    echo "  FAIL: docker not found" >&2
    failed=1
  fi

  if docker compose version >/dev/null 2>&1; then
    echo "  OK: $(docker compose version)"
  else
    echo "  FAIL: docker compose plugin not available" >&2
    failed=1
  fi

  if [ -f "${ENV_FILE}" ]; then
    echo "  OK: ${ENV_FILE} exists"
    if grep -q 'replace_with_' "${ENV_FILE}"; then
      echo "  FAIL: ${ENV_FILE} still contains placeholder secrets. Run: $0 setup --force" >&2
      failed=1
    fi
  else
    echo "  FAIL: ${ENV_FILE} missing. Run: $0 setup" >&2
    failed=1
  fi

  if [ "${failed}" -eq 0 ]; then
    if docker compose --env-file "${ENV_FILE}" -f "${COMPOSE_FILE}" config --quiet; then
      echo "  OK: Compose config valid"
    else
      echo "  FAIL: Compose config invalid" >&2
      failed=1
    fi
  fi

  return "${failed}"
}

cmd_up() {
  cmd_doctor
  echo "[up] Starting OpenKSM + IDNBase stack..."
  docker compose --env-file "${ENV_FILE}" -f "${COMPOSE_FILE}" up -d --build
}

cmd_down() {
  docker compose --env-file "${ENV_FILE}" -f "${COMPOSE_FILE}" down
}

cmd_status() {
  docker compose --env-file "${ENV_FILE}" -f "${COMPOSE_FILE}" ps
}

cmd_backup() {
  local ts dir
  ts="$(date -u +%Y%m%dT%H%M%SZ)"
  dir="${ROOT_DIR}/backups/${ts}"
  mkdir -p "${dir}"
  chmod 700 "${ROOT_DIR}/backups" "${dir}"

  echo "[backup] Dumping openksm_registry to ${dir}/openksm_registry.sql..."
  docker exec openksm-postgres-1 pg_dump -U idnbase_bootstrap -d openksm_registry > "${dir}/openksm_registry.sql"
  (cd "${dir}" && shasum -a 256 openksm_registry.sql > SHA256SUMS)
  echo "[backup] Backup complete and checksummed at ${dir}"
}

cmd_restore_drill() {
  local latest start_ts end_ts elapsed
  latest="$(ls -1d "${ROOT_DIR}"/backups/* 2>/dev/null | sort | tail -n 1 || true)"
  if [ -z "${latest}" ] || [ ! -f "${latest}/openksm_registry.sql" ]; then
    echo "FAIL: No backup found. Run: $0 backup" >&2
    exit 1
  fi
  echo "[restore-drill] Verifying checksum in ${latest}..."
  (cd "${latest}" && shasum -a 256 -c SHA256SUMS)

  start_ts="$(date +%s)"
  docker exec openksm-postgres-1 psql -U idnbase_bootstrap -d postgres -c "DROP DATABASE IF EXISTS openksm_restore_drill;"
  docker exec openksm-postgres-1 psql -U idnbase_bootstrap -d postgres -c "CREATE DATABASE openksm_restore_drill;"
  docker exec -i openksm-postgres-1 psql -U idnbase_bootstrap -d openksm_restore_drill < "${latest}/openksm_registry.sql" >/dev/null
  local org_count
  org_count="$(docker exec openksm-postgres-1 psql -U idnbase_bootstrap -d openksm_restore_drill -tAc "SELECT COUNT(*) FROM organizations;" | tr -d '[:space:]')"
  docker exec openksm-postgres-1 psql -U idnbase_bootstrap -d postgres -c "DROP DATABASE openksm_restore_drill;"
  end_ts="$(date +%s)"
  elapsed=$((end_ts - start_ts))
  echo "[restore-drill] PASS: Restored ${org_count} organizations into isolated DB in ${elapsed}s (RTO measured)."
}

cmd_upgrade_check() {
  local target="${1:-}"
  local lock_file="${ROOT_DIR}/deploy/manifest.lock.yaml"
  if [ -z "${target}" ]; then
    echo "Usage: $0 upgrade-check <idnbase_version>" >&2
    exit 1
  fi
  if ! grep -q "version: \"${target}\"" "${lock_file}"; then
    echo "BLOCKED: Target IDNBase version '${target}' is UNKNOWN in ${lock_file}." >&2
    exit 2
  fi
  echo "PASS: Target IDNBase version '${target}' is VERIFIED in manifest.lock.yaml."
}

case "${1:-}" in
  setup)
    cmd_setup "${2:-}"
    ;;
  doctor)
    cmd_doctor
    ;;
  up)
    cmd_up
    ;;
  down)
    cmd_down
    ;;
  status)
    cmd_status
    ;;
  backup)
    cmd_backup
    ;;
  restore-drill)
    cmd_restore_drill
    ;;
  upgrade-check)
    cmd_upgrade_check "${2:-}"
    ;;
  *)
    usage
    exit 1
    ;;
esac
