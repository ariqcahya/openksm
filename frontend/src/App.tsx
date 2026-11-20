import { FormEvent, useCallback, useEffect, useState } from 'react';
import { apiFetch, newIdempotencyKey } from './api';

type Tab = 'orgs' | 'members' | 'collections' | 'finance' | 'support' | 'tasks';

interface SessionUser {
  id: string;
  email: string;
  displayName: string;
}

interface Org {
  id: string;
  name: string;
  slug: string;
  org_type: string;
  region_code: string;
  status: string;
}

interface Member {
  id: string;
  email: string;
  status: string;
  roles: string[];
}

interface Customer {
  id: string;
  display_name: string;
  phone_masked?: string;
  status: string;
}

interface Invoice {
  id: string;
  billing_period: string;
  amount_idr: number;
  status: string;
}

interface LedgerSummary {
  net_balance_idr: number;
  cash_idr: number;
  receivable_idr: number;
  entry_count: number;
}

interface TaskItem {
  id: string;
  business_type: string;
  state: string;
  priority: string;
}

interface QueuedPickup {
  stop_id: string;
  event_type: 'completed' | 'failed_pickup' | 'rescheduled';
  idempotency_key: string;
  occurred_at: string;
  reason?: string;
}

const QUEUE_KEY = 'openksm_offline_pickup_queue_v1';

const cardStyle: React.CSSProperties = {
  background: '#ffffff',
  border: '1px solid #e2e8f0',
  borderRadius: 10,
  padding: 14,
  marginBottom: 14,
};

const inputStyle: React.CSSProperties = {
  width: '100%',
  boxSizing: 'border-box',
  padding: '8px 10px',
  borderRadius: 6,
  border: '1px solid #cbd5e1',
  marginBottom: 8,
  fontSize: '0.95rem',
};

const btnStyle: React.CSSProperties = {
  padding: '8px 14px',
  borderRadius: 6,
  border: 'none',
  background: '#0f766e',
  color: '#ffffff',
  cursor: 'pointer',
  fontWeight: 600,
};

export default function App() {
  const [health, setHealth] = useState<string>('checking...');
  const [user, setUser] = useState<SessionUser | null>(null);
  const [authChecked, setAuthChecked] = useState(false);

  // Login & MFA state
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [challengeToken, setChallengeToken] = useState('');
  const [mfaCode, setMfaCode] = useState('');
  const [authError, setAuthError] = useState('');

  // Workspace state
  const [tab, setTab] = useState<Tab>('orgs');
  const [orgs, setOrgs] = useState<Org[]>([]);
  const [selectedOrg, setSelectedOrg] = useState<string>('');
  const [members, setMembers] = useState<Member[]>([]);
  const [customers, setCustomers] = useState<Customer[]>([]);
  const [invoices, setInvoices] = useState<Invoice[]>([]);
  const [ledger, setLedger] = useState<LedgerSummary | null>(null);
  const [tasks, setTasks] = useState<TaskItem[]>([]);
  const [msg, setMsg] = useState('');

  // Form states
  const [orgName, setOrgName] = useState('');
  const [orgSlug, setOrgSlug] = useState('');
  const [orgRegion, setOrgRegion] = useState('ID-YO-SLM');
  const [inviteEmail, setInviteEmail] = useState('');
  const [inviteRole, setInviteRole] = useState('collection_officer');
  const [custName, setCustName] = useState('');
  const [custPhone, setCustPhone] = useState('');

  const [queue, setQueue] = useState<QueuedPickup[]>(() => {
    try {
      return JSON.parse(localStorage.getItem(QUEUE_KEY) || '[]');
    } catch {
      return [];
    }
  });

  const loadSessionAndOrgs = useCallback(async () => {
    try {
      const s = await apiFetch<{ user: SessionUser }>('/api/v1/auth/session');
      setUser(s.user);
      const o = await apiFetch<{ items: Org[] }>('/api/v1/organizations');
      const items = o.items || [];
      setOrgs(items);
      setSelectedOrg((prev) => prev || (items[0]?.id ?? ''));
    } catch {
      setUser(null);
    } finally {
      setAuthChecked(true);
    }
  }, []);

  useEffect(() => {
    fetch('/health/ready')
      .then((r) => r.json())
      .then((d) => setHealth(d.status || 'unknown'))
      .catch(() => setHealth('offline'));
    loadSessionAndOrgs();
  }, [loadSessionAndOrgs]);

  const loadOrgData = useCallback(async (orgId: string) => {
    if (!orgId) return;
    setMsg('');
    const results = await Promise.allSettled([
      apiFetch<{ items: Member[] }>(`/api/v1/organizations/${orgId}/memberships`),
      apiFetch<{ items: Customer[] }>(`/api/v1/organizations/${orgId}/customers`),
      apiFetch<{ items: Invoice[] }>(`/api/v1/organizations/${orgId}/invoices`),
      apiFetch<LedgerSummary>(`/api/v1/organizations/${orgId}/ledger/summary`),
      apiFetch<{ items: TaskItem[] }>(`/api/v1/organizations/${orgId}/tasks`),
    ]);
    if (results[0].status === 'fulfilled') setMembers(results[0].value.items || []);
    if (results[1].status === 'fulfilled') setCustomers(results[1].value.items || []);
    if (results[2].status === 'fulfilled') setInvoices(results[2].value.items || []);
    if (results[3].status === 'fulfilled') setLedger(results[3].value);
    if (results[4].status === 'fulfilled') setTasks(results[4].value.items || []);
  }, []);

  useEffect(() => {
    if (user && selectedOrg) {
      loadOrgData(selectedOrg);
    }
  }, [user, selectedOrg, loadOrgData]);

  const handleLogin = async (e: FormEvent) => {
    e.preventDefault();
    setAuthError('');
    try {
      const res = await apiFetch<{ mfaRequired?: boolean; challengeToken?: string }>('/api/v1/auth/login', {
        method: 'POST',
        body: JSON.stringify({ email, password }),
      });
      if (res.mfaRequired && res.challengeToken) {
        setChallengeToken(res.challengeToken);
      } else {
        await loadSessionAndOrgs();
      }
    } catch (err) {
      setAuthError(err instanceof Error ? err.message : 'Login failed');
    }
  };

  const handleMFA = async (e: FormEvent) => {
    e.preventDefault();
    setAuthError('');
    try {
      await apiFetch('/api/v1/auth/mfa/verify', {
        method: 'POST',
        body: JSON.stringify({ challengeToken, code: mfaCode }),
      });
      setChallengeToken('');
      setMfaCode('');
      setPassword('');
      await loadSessionAndOrgs();
    } catch (err) {
      setAuthError(err instanceof Error ? err.message : 'MFA verification failed');
    }
  };

  const handleLogout = async () => {
    await apiFetch('/api/v1/auth/logout', { method: 'POST' });
    setUser(null);
    setOrgs([]);
    setSelectedOrg('');
  };

  const handleApplyOrg = async (e: FormEvent) => {
    e.preventDefault();
    setMsg('');
    try {
      await apiFetch('/api/v1/organizations', {
        method: 'POST',
        headers: { 'Idempotency-Key': newIdempotencyKey('org') },
        body: JSON.stringify({ name: orgName, slug: orgSlug, org_type: 'ksm', region_code: orgRegion }),
      });
      setOrgName('');
      setOrgSlug('');
      setMsg('Organization application submitted.');
      await loadSessionAndOrgs();
    } catch (err) {
      setMsg(err instanceof Error ? err.message : 'Failed');
    }
  };

  const handleInvite = async (e: FormEvent) => {
    e.preventDefault();
    if (!selectedOrg) return;
    setMsg('');
    try {
      await apiFetch(`/api/v1/organizations/${selectedOrg}/memberships`, {
        method: 'POST',
        body: JSON.stringify({ email: inviteEmail, role_code: inviteRole }),
      });
      setInviteEmail('');
      setMsg('Member invited.');
      await loadOrgData(selectedOrg);
    } catch (err) {
      setMsg(err instanceof Error ? err.message : 'Failed');
    }
  };

  const handleCreateCustomer = async (e: FormEvent) => {
    e.preventDefault();
    if (!selectedOrg) return;
    setMsg('');
    try {
      await apiFetch(`/api/v1/organizations/${selectedOrg}/customers`, {
        method: 'POST',
        body: JSON.stringify({ display_name: custName, phone_masked: custPhone }),
      });
      setCustName('');
      setCustPhone('');
      setMsg('Customer created.');
      await loadOrgData(selectedOrg);
    } catch (err) {
      setMsg(err instanceof Error ? err.message : 'Failed');
    }
  };

  const queueOfflinePickup = (stopId: string, eventType: QueuedPickup['event_type']) => {
    const next: QueuedPickup[] = [
      ...queue,
      {
        stop_id: stopId,
        event_type: eventType,
        idempotency_key: newIdempotencyKey('pwa-sync'),
        occurred_at: new Date().toISOString(),
      },
    ];
    setQueue(next);
    localStorage.setItem(QUEUE_KEY, JSON.stringify(next));
  };

  const syncOfflineQueue = async () => {
    if (!selectedOrg || queue.length === 0) return;
    setMsg('Syncing...');
    try {
      const data = await apiFetch<{ results: unknown[] }>(`/api/v1/organizations/${selectedOrg}/collections/sync`, {
        method: 'POST',
        body: JSON.stringify({ events: queue }),
      });
      setQueue([]);
      localStorage.removeItem(QUEUE_KEY);
      setMsg(`Synced: ${JSON.stringify(data.results)}`);
      await loadOrgData(selectedOrg);
    } catch {
      setMsg('Network unavailable; kept in offline queue.');
    }
  };

  if (!authChecked) {
    return <div style={{ padding: 20, fontFamily: 'system-ui, sans-serif' }}>Loading OpenKSM...</div>;
  }

  if (!user) {
    return (
      <div style={{ fontFamily: 'system-ui, sans-serif', maxWidth: 420, margin: '40px auto', padding: 12 }}>
        <div style={cardStyle}>
          <h1 style={{ marginTop: 0, fontSize: '1.35rem' }}>OpenKSM Sign In</h1>
          <p style={{ color: '#475569', fontSize: '0.9rem' }}>API Status: <strong>{health}</strong></p>
          {authError && <p style={{ color: '#b91c1c', fontSize: '0.9rem' }}>{authError}</p>}
          {!challengeToken ? (
            <form onSubmit={handleLogin}>
              <label>Email</label>
              <input style={inputStyle} type="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
              <label>Password</label>
              <input style={inputStyle} type="password" required value={password} onChange={(e) => setPassword(e.target.value)} />
              <button style={btnStyle} type="submit">Sign In</button>
            </form>
          ) : (
            <form onSubmit={handleMFA}>
              <label>6-Digit TOTP MFA Code</label>
              <input style={inputStyle} type="text" inputMode="numeric" maxLength={6} required value={mfaCode} onChange={(e) => setMfaCode(e.target.value)} />
              <button style={btnStyle} type="submit">Verify MFA</button>
            </form>
          )}
        </div>
      </div>
    );
  }

  return (
    <div style={{ fontFamily: 'system-ui, sans-serif', maxWidth: 880, margin: '0 auto', padding: 12, color: '#0f172a', background: '#f8fafc', minHeight: '100vh' }}>
      <header style={{ display: 'flex', flexWrap: 'wrap', justifyContent: 'space-between', alignItems: 'center', gap: 8, borderBottom: '1px solid #e2e8f0', paddingBottom: 12 }}>
        <div>
          <h1 style={{ margin: 0, fontSize: '1.25rem' }}>OpenKSM</h1>
          <small style={{ color: '#475569' }}>{user.email} · API: {health}</small>
        </div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
          {orgs.length > 0 && (
            <select value={selectedOrg} onChange={(e) => setSelectedOrg(e.target.value)} aria-label="Organization" style={{ padding: '6px 8px', borderRadius: 6 }}>
              {orgs.map((o) => (
                <option key={o.id} value={o.id}>{o.name} ({o.status})</option>
              ))}
            </select>
          )}
          <button onClick={handleLogout} style={{ padding: '6px 10px', borderRadius: 6, border: '1px solid #cbd5e1', background: '#fff', cursor: 'pointer' }}>
            Sign out
          </button>
        </div>
      </header>

      <nav style={{ display: 'flex', flexWrap: 'wrap', gap: 8, margin: '12px 0' }}>
        {(['orgs', 'members', 'collections', 'finance', 'support', 'tasks'] as Tab[]).map((t) => (
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
              fontSize: '0.85rem',
            }}
          >
            {t.toUpperCase()}
          </button>
        ))}
      </nav>

      {msg && <div style={{ ...cardStyle, background: '#f0fdfa', borderColor: '#99f6e4' }}><small>{msg}</small></div>}

      {tab === 'orgs' && (
        <div style={cardStyle}>
          <h2 style={{ marginTop: 0 }}>Organizations (M02)</h2>
          <ul>
            {orgs.map((o) => (
              <li key={o.id}>{o.name} — <code>{o.slug}</code> [{o.region_code}] ({o.status})</li>
            ))}
          </ul>
          <h3>Apply New Organization</h3>
          <form onSubmit={handleApplyOrg}>
            <input style={inputStyle} placeholder="Organization Name" required value={orgName} onChange={(e) => setOrgName(e.target.value)} />
            <input style={inputStyle} placeholder="slug-lowercase-only" required value={orgSlug} onChange={(e) => setOrgSlug(e.target.value)} />
            <input style={inputStyle} placeholder="Region Code (e.g. ID-YO-SLM)" required value={orgRegion} onChange={(e) => setOrgRegion(e.target.value)} />
            <button style={btnStyle} type="submit">Submit Application</button>
          </form>
        </div>
      )}

      {tab === 'members' && (
        <div style={cardStyle}>
          <h2 style={{ marginTop: 0 }}>Memberships (M02)</h2>
          <ul>
            {members.map((m) => (
              <li key={m.id}>{m.email} — <strong>{m.roles.join(', ')}</strong> ({m.status})</li>
            ))}
          </ul>
          <h3>Invite Member</h3>
          <form onSubmit={handleInvite}>
            <input style={inputStyle} type="email" placeholder="member@example.org" required value={inviteEmail} onChange={(e) => setInviteEmail(e.target.value)} />
            <select style={inputStyle} value={inviteRole} onChange={(e) => setInviteRole(e.target.value)}>
              <option value="org_admin">org_admin</option>
              <option value="treasurer">treasurer</option>
              <option value="billing_officer">billing_officer</option>
              <option value="cashier">cashier</option>
              <option value="ops_manager">ops_manager</option>
              <option value="collection_officer">collection_officer</option>
            </select>
            <button style={btnStyle} type="submit">Invite</button>
          </form>
        </div>
      )}

      {tab === 'collections' && (
        <div style={cardStyle}>
          <h2 style={{ marginTop: 0 }}>Customers &amp; Offline Sync (M03)</h2>
          <ul>
            {customers.map((c) => (
              <li key={c.id}>{c.display_name} ({c.phone_masked || 'no phone'})</li>
            ))}
          </ul>
          <h3>Add Customer</h3>
          <form onSubmit={handleCreateCustomer}>
            <input style={inputStyle} placeholder="Customer Display Name" required value={custName} onChange={(e) => setCustName(e.target.value)} />
            <input style={inputStyle} placeholder="Masked Phone (e.g. 0812****8899)" value={custPhone} onChange={(e) => setCustPhone(e.target.value)} />
            <button style={btnStyle} type="submit">Create Customer</button>
          </form>
          <hr style={{ margin: '16px 0', borderColor: '#e2e8f0' }} />
          <p>Offline Pickup Queue: <strong>{queue.length}</strong></p>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
            <button style={btnStyle} type="button" onClick={() => queueOfflinePickup('00000000-0000-0000-0000-000000000001', 'completed')}>
              Queue Sample Pickup
            </button>
            <button style={btnStyle} type="button" onClick={syncOfflineQueue} disabled={queue.length === 0 || !selectedOrg}>
              Sync Offline Queue
            </button>
          </div>
        </div>
      )}

      {tab === 'finance' && (
        <div style={cardStyle}>
          <h2 style={{ marginTop: 0 }}>Billing &amp; Balanced Ledger (M04)</h2>
          {ledger && (
            <p>
              <strong>Net Balance:</strong> {ledger.net_balance_idr} IDR | <strong>Cash:</strong> {ledger.cash_idr} IDR | <strong>AR:</strong> {ledger.receivable_idr} IDR ({ledger.entry_count} entries)
            </p>
          )}
          <h3>Invoices</h3>
          {invoices.length === 0 ? <p>No invoices yet.</p> : (
            <ul>
              {invoices.map((inv) => (
                <li key={inv.id}>{inv.billing_period} — Rp {inv.amount_idr.toLocaleString('id-ID')} ({inv.status})</li>
              ))}
            </ul>
          )}
        </div>
      )}

      {tab === 'support' && (
        <div style={cardStyle}>
          <h2 style={{ marginTop: 0 }}>Complaints &amp; Scoped Reports (M05)</h2>
          <p><small>Endpoints: <code>/complaints</code>, <code>/complaints/:id/transitions</code>, <code>/reports/summary?from=...&amp;to=...</code> (PII omitted)</small></p>
        </div>
      )}

      {tab === 'tasks' && (
        <div style={cardStyle}>
          <h2 style={{ marginTop: 0 }}>Task Inbox (M03)</h2>
          {tasks.length === 0 ? <p>No open tasks.</p> : (
            <ul>
              {tasks.map((t) => (
                <li key={t.id}>[{t.priority}] {t.business_type} — {t.state}</li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}
