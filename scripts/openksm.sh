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

  local pg_pass master_key jwt_secret
  pg_pass="$(gen_hex 24)"
  master_key="$(gen_hex 32)"
  jwt_secret="$(gen_hex 32)"

  sed \
    -e "s|^POSTGRES_BOOTSTRAP_PASSWORD=.*|POSTGRES_BOOTSTRAP_PASSWORD=${pg_pass}|" \
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
  *)
    usage
    exit 1
    ;;
esac
