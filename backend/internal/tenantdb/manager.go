package tenantdb

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openksm/openksm/backend/migrations"
)

// Manager resolves and caches isolated per-organization PostgreSQL pools (p_<uuid>).
type Manager struct {
	registryPool *pgxpool.Pool
	baseDSN      string // e.g. postgres://user:pass@postgres:5432?sslmode=disable
	mu           sync.RWMutex
	pools        map[string]*pgxpool.Pool
}

// NewManager creates a tenant DB pool manager.
func NewManager(registryPool *pgxpool.Pool, baseDSN string) *Manager {
	return &Manager{
		registryPool: registryPool,
		baseDSN:      strings.TrimSpace(baseDSN),
		pools:        make(map[string]*pgxpool.Pool),
	}
}

// PoolForOrg returns a migrated pgxpool.Pool for the organization's isolated p_<uuid> database.
// If baseDSN is empty (e.g. minimal dev mode), it falls back to registryPool.
func (m *Manager) PoolForOrg(ctx context.Context, orgID string) (*pgxpool.Pool, error) {
	if m.baseDSN == "" {
		return m.registryPool, nil
	}

	m.mu.RLock()
	if p, ok := m.pools[orgID]; ok {
		m.mu.RUnlock()
		return p, nil
	}
	m.mu.RUnlock()

	var projectID string
	err := m.registryPool.QueryRow(ctx, `
		SELECT idnbase_project_id::text
		FROM database_refs
		WHERE organization_id = $1::uuid
	`, orgID).Scan(&projectID)
	if err != nil {
		return nil, fmt.Errorf("organization database not provisioned: %w", err)
	}

	dbName := "p_" + strings.ReplaceAll(projectID, "-", "_")
	u, err := url.Parse(m.baseDSN)
	if err != nil {
		return nil, err
	}
	u.Path = "/" + dbName

	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.pools[orgID]; ok {
		return p, nil
	}

	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}

	for _, mig := range []string{"tenant/001_init_tenant.sql", "tenant/002_m03_collections.sql", "tenant/003_m04_finance.sql"} {
		sqlBytes, err := migrations.FS.ReadFile(mig)
		if err != nil {
			pool.Close()
			return nil, err
		}
		if _, err := pool.Exec(ctx, string(sqlBytes)); err != nil {
			pool.Close()
			return nil, err
		}
	}

	m.pools[orgID] = pool
	return pool, nil
}

// Close closes all cached tenant pools.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.pools {
		p.Close()
	}
}
