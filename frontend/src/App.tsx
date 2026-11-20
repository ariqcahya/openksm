import { useEffect, useState } from 'react';

type Tab = 'orgs' | 'members' | 'collections' | 'tasks';

interface QueuedPickup {
  stop_id: string;
  event_type: 'completed' | 'failed_pickup' | 'rescheduled';
  idempotency_key: string;
  occurred_at: string;
  reason?: string;
}

const QUEUE_KEY = 'openksm_offline_pickup_queue_v1';

export default function App() {
  const [tab, setTab] = useState<Tab>('orgs');
  const [health, setHealth] = useState<string>('checking...');
  const [orgs, setOrgs] = useState<Array<{ id: string; name: string; slug: string; region_code: string; status: string }>>([]);
  const [selectedOrg, setSelectedOrg] = useState<string>('');
  const [queue, setQueue] = useState<QueuedPickup[]>(() => {
    try {
      return JSON.parse(localStorage.getItem(QUEUE_KEY) || '[]');
    } catch {
      return [];
    }
  });
  const [syncMsg, setSyncMsg] = useState<string>('');

  useEffect(() => {
    fetch('/health/ready')
      .then((r) => r.json())
      .then((d) => setHealth(d.status || 'unknown'))
      .catch(() => setHealth('offline'));

    fetch('/api/v1/organizations')
      .then((r) => (r.ok ? r.json() : { items: [] }))
      .then((d) => {
        const items = d.items || [];
        setOrgs(items);
        if (items.length > 0 && !selectedOrg) setSelectedOrg(items[0].id);
      })
      .catch(() => {});
  }, [selectedOrg]);

  const queueOfflinePickup = (stopId: string, eventType: QueuedPickup['event_type'], reason: string) => {
    const next: QueuedPickup[] = [
      ...queue,
      {
        stop_id: stopId,
        event_type: eventType,
        idempotency_key: `pwa-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`,
        occurred_at: new Date().toISOString(),
        reason: reason || undefined,
      },
    ];
    setQueue(next);
    localStorage.setItem(QUEUE_KEY, JSON.stringify(next));
  };

  const syncOfflineQueue = async () => {
    if (!selectedOrg || queue.length === 0) return;
    setSyncMsg('Syncing...');
    try {
      const res = await fetch(`/api/v1/organizations/${selectedOrg}/collections/sync`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ events: queue }),
      });
      if (!res.ok) {
        setSyncMsg(`Sync failed (${res.status})`);
        return;
      }
      const data = await res.json();
      setQueue([]);
      localStorage.removeItem(QUEUE_KEY);
      setSyncMsg(`Synced: ${JSON.stringify(data.results)}`);
    } catch {
      setSyncMsg('Network unavailable; kept in offline queue.');
    }
  };

  return (
    <div style={{ fontFamily: 'system-ui, sans-serif', maxWidth: 840, margin: '0 auto', padding: '12px', color: '#0f172a' }}>
      <header style={{ display: 'flex', flexWrap: 'wrap', justifyContent: 'space-between', alignItems: 'center', gap: 8, borderBottom: '1px solid #e2e8f0', paddingBottom: 12 }}>
        <div>
          <h1 style={{ margin: 0, fontSize: '1.25rem' }}>OpenKSM</h1>
          <small style={{ color: '#475569' }}>API: {health}</small>
        </div>
        {orgs.length > 0 && (
          <select value={selectedOrg} onChange={(e) => setSelectedOrg(e.target.value)} aria-label="Organization" style={{ padding: '6px 8px', borderRadius: 6 }}>
            {orgs.map((o) => (
              <option key={o.id} value={o.id}>{o.name} ({o.status})</option>
            ))}
          </select>
        )}
      </header>

      <nav style={{ display: 'flex', flexWrap: 'wrap', gap: 8, margin: '12px 0' }}>
        {(['orgs', 'members', 'collections', 'tasks'] as Tab[]).map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            style={{
              padding: '8px 12px',
              borderRadius: 6,
              border: '1px solid #cbd5e1',
              background: tab === t ? '#0f766e' : '#ffffff',
              color: tab === t ? '#ffffff' : '#0f172a',
              cursor: 'pointer',
            }}
          >
            {t.toUpperCase()}
          </button>
        ))}
      </nav>

      {tab === 'orgs' && (
        <section>
          <h2>Organizations (M02)</h2>
          {orgs.length === 0 ? <p>Sign in via session or no organizations yet.</p> : (
            <ul>
              {orgs.map((o) => (
                <li key={o.id}>{o.name} — <code>{o.slug}</code> [{o.region_code}] ({o.status})</li>
              ))}
            </ul>
          )}
        </section>
      )}

      {tab === 'members' && (
        <section>
          <h2>Memberships &amp; Service Areas (M02)</h2>
          <p>Selected Organization ID: <code>{selectedOrg || 'none'}</code></p>
        </section>
      )}

      {tab === 'collections' && (
        <section>
          <h2>Field Collections &amp; Offline Sync (M03)</h2>
          <p>Queued offline pickup events: <strong>{queue.length}</strong></p>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
            <button onClick={() => queueOfflinePickup('00000000-0000-0000-0000-000000000001', 'completed', '')}>
              Queue Sample Pickup
            </button>
            <button onClick={syncOfflineQueue} disabled={queue.length === 0 || !selectedOrg}>
              Sync Offline Queue
            </button>
          </div>
          {syncMsg && <p><small>{syncMsg}</small></p>}
        </section>
      )}

      {tab === 'tasks' && (
        <section>
          <h2>Task Inbox (M03)</h2>
          <p>Scoped tasks for organization: <code>{selectedOrg || 'none'}</code></p>
        </section>
      )}
    </div>
  );
}
