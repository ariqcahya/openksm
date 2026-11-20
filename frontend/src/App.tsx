import { FormEvent, useCallback, useEffect, useState } from 'react';
import { apiFetch, newIdempotencyKey } from './api';

type Tab = 'home' | 'orgs' | 'field' | 'finance' | 'support' | 'waste' | 'tasks';

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

interface ComplaintItem {
  id: string;
  title: string;
  state: string;
  due_at: string;
}

interface ReportSummary {
  from_date: string;
  to_date: string;
  customer_count: number;
  collection_job_count: number;
  open_complaints: number;
  resolved_complaints: number;
  total_billed_idr: number;
  posted_payments_idr: number;
  cash_balance_idr: number;
}

interface InventoryItem {
  material_category: string;
  available_kg: number;
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

const ROLE_LABELS: Record<string, string> = {
  org_owner: 'Ketua Organisasi',
  org_admin: 'Admin Organisasi',
  treasurer: 'Bendahara',
  billing_officer: 'Petugas Tagihan',
  cashier: 'Kasir',
  ops_manager: 'Manajer Operasi',
  collection_officer: 'Petugas Lapangan',
  customer_service: 'Layanan Pelanggan (CS)',
  org_auditor: 'Auditor Internal',
};

const QUEUE_KEY = 'openksm_offline_pickup_queue_v1';

const cardStyle: React.CSSProperties = {
  background: '#ffffff',
  border: '1px solid #e2e8f0',
  borderRadius: 12,
  padding: 16,
  marginBottom: 14,
};

const inputStyle: React.CSSProperties = {
  width: '100%',
  boxSizing: 'border-box',
  padding: '10px 12px',
  borderRadius: 8,
  border: '1px solid #cbd5e1',
  marginBottom: 10,
  fontSize: '16px',
  minHeight: 44,
};

const btnStyle: React.CSSProperties = {
  padding: '10px 16px',
  borderRadius: 8,
  border: 'none',
  background: '#0f766e',
  color: '#ffffff',
  cursor: 'pointer',
  fontWeight: 600,
  minHeight: 44,
};

export default function App() {
  const [health, setHealth] = useState<string>('memeriksa...');
  const [user, setUser] = useState<SessionUser | null>(null);
  const [authChecked, setAuthChecked] = useState(false);

  // Auth forms
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [challengeToken, setChallengeToken] = useState('');
  const [mfaCode, setMfaCode] = useState('');
  const [authError, setAuthError] = useState('');

  // Org & Role Context
  const [tab, setTab] = useState<Tab>('home');
  const [orgs, setOrgs] = useState<Org[]>([]);
  const [selectedOrg, setSelectedOrg] = useState<string>('');
  const [myRoles, setMyRoles] = useState<string[]>([]);
  const [myPerms, setMyPerms] = useState<Set<string>>(new Set());

  // Domain data
  const [members, setMembers] = useState<Member[]>([]);
  const [customers, setCustomers] = useState<Customer[]>([]);
  const [invoices, setInvoices] = useState<Invoice[]>([]);
  const [ledger, setLedger] = useState<LedgerSummary | null>(null);
  const [complaints, setComplaints] = useState<ComplaintItem[]>([]);
  const [report, setReport] = useState<ReportSummary | null>(null);
  const [inventory, setInventory] = useState<InventoryItem[]>([]);
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
  const [cmpTitle, setCmpTitle] = useState('');
  const [cmpDesc, setCmpDesc] = useState('');
  const [intakeCat, setIntakeCat] = useState('PET');
  const [intakeKg, setIntakeKg] = useState('10');

  const [queue, setQueue] = useState<QueuedPickup[]>(() => {
    try {
      return JSON.parse(localStorage.getItem(QUEUE_KEY) || '[]');
    } catch {
      return [];
    }
  });

  const can = (perm: string) => myPerms.has(perm);

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
      .then((d) => setHealth(d.status === 'ready' ? 'Sistem Siap' : 'Gangguan'))
      .catch(() => setHealth('Offline'));
    loadSessionAndOrgs();
  }, [loadSessionAndOrgs]);

  const loadOrgData = useCallback(async (orgId: string) => {
    if (!orgId) return;
    setMsg('');
    try {
      const me = await apiFetch<{ roles: string[]; permissions: string[] }>(`/api/v1/organizations/${orgId}/me`);
      setMyRoles(me.roles || []);
      const permSet = new Set(me.permissions || []);
      setMyPerms(permSet);

      const calls: Array<Promise<void>> = [];
      if (permSet.has('membership:invite')) {
        calls.push(apiFetch<{ items: Member[] }>(`/api/v1/organizations/${orgId}/memberships`).then((r) => setMembers(r.items || [])));
      }
      if (permSet.has('customer:manage')) {
        calls.push(apiFetch<{ items: Customer[] }>(`/api/v1/organizations/${orgId}/customers`).then((r) => setCustomers(r.items || [])));
      }
      if (permSet.has('finance:read') || permSet.has('billing:manage')) {
        calls.push(apiFetch<{ items: Invoice[] }>(`/api/v1/organizations/${orgId}/invoices`).then((r) => setInvoices(r.items || [])));
        calls.push(apiFetch<LedgerSummary>(`/api/v1/organizations/${orgId}/ledger/summary`).then((r) => setLedger(r)));
      }
      if (permSet.has('complaint:manage')) {
        calls.push(apiFetch<{ items: ComplaintItem[] }>(`/api/v1/organizations/${orgId}/complaints`).then((r) => setComplaints(r.items || [])));
      }
      if (permSet.has('inventory:move')) {
        calls.push(apiFetch<{ items: InventoryItem[] }>(`/api/v1/organizations/${orgId}/inventory`).then((r) => setInventory(r.items || [])));
      }
      if (permSet.has('task:read')) {
        calls.push(apiFetch<{ items: TaskItem[] }>(`/api/v1/organizations/${orgId}/tasks`).then((r) => setTasks(r.items || [])));
      }
      await Promise.allSettled(calls);
    } catch {
      setMyRoles([]);
      setMyPerms(new Set());
    }
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
    } catch {
      setAuthError('Email atau kata sandi tidak sesuai. Silakan periksa kembali.');
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
    } catch {
      setAuthError('Kode Authenticator salah atau kedaluwarsa. Coba kode terbaru.');
    }
  };

  const handleLogout = async () => {
    await apiFetch('/api/v1/auth/logout', { method: 'POST' });
    setUser(null);
    setOrgs([]);
    setSelectedOrg('');
  };

  const queueOfflinePickup = (stopId: string, eventType: QueuedPickup['event_type'], reason?: string) => {
    const next: QueuedPickup[] = [
      ...queue,
      {
        stop_id: stopId,
        event_type: eventType,
        idempotency_key: newIdempotencyKey('pwa-sync'),
        occurred_at: new Date().toISOString(),
        reason,
      },
    ];
    setQueue(next);
    localStorage.setItem(QUEUE_KEY, JSON.stringify(next));
    setMsg('Tersimpan di antrean offline. Klik "Sinkronkan Sekarang" saat sinyal tersedia.');
  };

  const syncOfflineQueue = async () => {
    if (!selectedOrg || queue.length === 0) return;
    setMsg('Menyinkronkan antrean lapangan...');
    try {
      await apiFetch(`/api/v1/organizations/${selectedOrg}/collections/sync`, {
        method: 'POST',
        body: JSON.stringify({ events: queue }),
      });
      setQueue([]);
      localStorage.removeItem(QUEUE_KEY);
      setMsg('Seluruh catatan penjemputan berhasil disinkronkan.');
      await loadOrgData(selectedOrg);
    } catch {
      setMsg('Koneksi belum stabil. Data tetap aman tersimpan di perangkat.');
    }
  };

  if (!authChecked) {
    return <div style={{ padding: 24, fontFamily: 'system-ui, sans-serif' }}>Memuat OpenKSM...</div>;
  }

  if (!user) {
    return (
      <div style={{ fontFamily: 'system-ui, sans-serif', maxWidth: 420, margin: '36px auto', padding: 12 }}>
        <div style={cardStyle}>
          <h1 style={{ marginTop: 0, fontSize: '1.35rem', color: '#0f766e' }}>Masuk ke OpenKSM</h1>
          <p style={{ color: '#475569', fontSize: '0.9rem' }}>Status Layanan: <strong>{health}</strong></p>
          {authError && <p style={{ color: '#991b1b', background: '#fee2e2', padding: 10, borderRadius: 8, fontSize: '0.9rem' }}>{authError}</p>}
          {!challengeToken ? (
            <form onSubmit={handleLogin}>
              <label htmlFor="email">Email</label>
              <input id="email" style={inputStyle} type="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
              <label htmlFor="password">Kata Sandi</label>
              <input id="password" style={inputStyle} type="password" required value={password} onChange={(e) => setPassword(e.target.value)} />
              <button style={{ ...btnStyle, width: '100%' }} type="submit">Masuk</button>
            </form>
          ) : (
            <form onSubmit={handleMFA}>
              <label htmlFor="mfa">Kode Authenticator (6 digit)</label>
              <input id="mfa" style={inputStyle} type="text" inputMode="numeric" maxLength={6} required autoFocus value={mfaCode} onChange={(e) => setMfaCode(e.target.value)} />
              <button style={{ ...btnStyle, width: '100%' }} type="submit">Verifikasi &amp; Masuk</button>
            </form>
          )}
        </div>
      </div>
    );
  }

  const visibleTabs: Array<{ id: Tab; label: string }> = [
    { id: 'home', label: 'Beranda' },
    ...(can('organization:manage') || can('membership:invite') ? [{ id: 'orgs' as Tab, label: 'Organisasi & Anggota' }] : []),
    ...(can('customer:manage') || can('collection:execute') ? [{ id: 'field' as Tab, label: 'Pelanggan & Lapangan' }] : []),
    ...(can('finance:read') || can('billing:manage') || can('payment:record') ? [{ id: 'finance' as Tab, label: 'Tagihan & Keuangan' }] : []),
    ...(can('complaint:manage') || can('report:export') ? [{ id: 'support' as Tab, label: 'Pengaduan & Laporan' }] : []),
    ...(can('waste:weigh') || can('inventory:move') || can('sales:manage') ? [{ id: 'waste' as Tab, label: 'TPS3R & Stok' }] : []),
    ...(can('task:read') ? [{ id: 'tasks' as Tab, label: `Kotak Tugas (${tasks.length})` }] : []),
  ];

  const totalStokKG = inventory.reduce((acc, i) => acc + i.available_kg, 0);

  return (
    <div style={{ fontFamily: 'system-ui, sans-serif', maxWidth: 920, margin: '0 auto', padding: 12, color: '#0f172a', background: '#f8fafc', minHeight: '100vh' }}>
      <header style={{ display: 'flex', flexWrap: 'wrap', justifyContent: 'space-between', alignItems: 'center', gap: 10, borderBottom: '1px solid #e2e8f0', paddingBottom: 12 }}>
        <div>
          <h1 style={{ margin: 0, fontSize: '1.25rem', color: '#0f766e' }}>OpenKSM</h1>
          <small style={{ color: '#475569' }}>
            {user.email} · {myRoles.map((r) => ROLE_LABELS[r] || r).join(', ') || 'Pengguna Baru'} · {health}
          </small>
        </div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
          {orgs.length > 0 && (
            <select value={selectedOrg} onChange={(e) => setSelectedOrg(e.target.value)} aria-label="Pilih Organisasi" style={{ padding: '8px 10px', borderRadius: 8, minHeight: 44 }}>
              {orgs.map((o) => (
                <option key={o.id} value={o.id}>{o.name}</option>
              ))}
            </select>
          )}
          <button onClick={handleLogout} style={{ padding: '8px 14px', borderRadius: 8, border: '1px solid #cbd5e1', background: '#fff', cursor: 'pointer', minHeight: 44 }}>
            Keluar
          </button>
        </div>
      </header>

      <nav style={{ display: 'flex', flexWrap: 'wrap', gap: 8, margin: '12px 0' }}>
        {visibleTabs.map((t) => (
          <button
            key={t.id}
            onClick={() => setTab(t.id)}
            style={{
              padding: '10px 14px',
              borderRadius: 8,
              border: '1px solid #cbd5e1',
              background: tab === t.id ? '#0f766e' : '#ffffff',
              color: tab === t.id ? '#ffffff' : '#0f172a',
              cursor: 'pointer',
              fontSize: '0.9rem',
              fontWeight: 600,
              minHeight: 44,
            }}
          >
            {t.label}
          </button>
        ))}
      </nav>

      {msg && <div style={{ ...cardStyle, background: '#f0fdfa', borderColor: '#5eead4' }}>{msg}</div>}

      {tab === 'home' && (
        <div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', gap: 12, marginBottom: 14 }}>
            {can('task:read') && (
              <div style={cardStyle}>
                <small style={{ color: '#475569' }}>Tugas Terbuka</small>
                <div style={{ fontSize: '1.5rem', fontWeight: 700 }}>{tasks.length}</div>
              </div>
            )}
            {(can('finance:read') || can('billing:manage')) && (
              <div style={cardStyle}>
                <small style={{ color: '#475569' }}>Tagihan Tercatat</small>
                <div style={{ fontSize: '1.5rem', fontWeight: 700 }}>{invoices.length}</div>
              </div>
            )}
            {can('complaint:manage') && (
              <div style={cardStyle}>
                <small style={{ color: '#475569' }}>Pengaduan Aktif</small>
                <div style={{ fontSize: '1.5rem', fontWeight: 700 }}>{complaints.filter((c) => c.state !== 'closed').length}</div>
              </div>
            )}
            {can('inventory:move') && (
              <div style={cardStyle}>
                <small style={{ color: '#475569' }}>Stok Material</small>
                <div style={{ fontSize: '1.5rem', fontWeight: 700 }}>{totalStokKG} kg</div>
              </div>
            )}
          </div>
          <div style={cardStyle}>
            <h2 style={{ marginTop: 0 }}>Selamat Datang di OpenKSM</h2>
            <p style={{ color: '#475569' }}>
              Menu di atas disesuaikan otomatis dengan peran Anda ({myRoles.map((r) => ROLE_LABELS[r] || r).join(', ') || 'Belum ada peran'}).
            </p>
          </div>
        </div>
      )}

      {tab === 'orgs' && (
        <div style={cardStyle}>
          <h2 style={{ marginTop: 0 }}>Anggota Organisasi</h2>
          {members.length === 0 ? <p>Belum ada anggota terdaftar.</p> : (
            <ul>
              {members.map((m) => (
                <li key={m.id} style={{ marginBottom: 6 }}>
                  {m.email} — <strong>{m.roles.map((r) => ROLE_LABELS[r] || r).join(', ')}</strong>
                </li>
              ))}
            </ul>
          )}
          {can('membership:invite') && (
            <form onSubmit={async (e) => {
              e.preventDefault();
              await apiFetch(`/api/v1/organizations/${selectedOrg}/memberships`, {
                method: 'POST',
                body: JSON.stringify({ email: inviteEmail, role_code: inviteRole }),
              });
              setInviteEmail('');
              setMsg('Anggota baru berhasil diundang.');
              await loadOrgData(selectedOrg);
            }}>
              <h3>Undang Anggota Baru</h3>
              <input style={inputStyle} type="email" placeholder="Email anggota" required value={inviteEmail} onChange={(e) => setInviteEmail(e.target.value)} />
              <select style={inputStyle} value={inviteRole} onChange={(e) => setInviteRole(e.target.value)}>
                {Object.entries(ROLE_LABELS).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
              <button style={btnStyle} type="submit">Undang Anggota</button>
            </form>
          )}
          <hr style={{ margin: '18px 0', borderColor: '#e2e8f0' }} />
          <h3>Ajukan Organisasi Baru</h3>
          <form onSubmit={async (e) => {
            e.preventDefault();
            await apiFetch('/api/v1/organizations', {
              method: 'POST',
              headers: { 'Idempotency-Key': newIdempotencyKey('org') },
              body: JSON.stringify({ name: orgName, slug: orgSlug, org_type: 'ksm', region_code: orgRegion }),
            });
            setOrgName('');
            setOrgSlug('');
            setMsg('Pengajuan organisasi terkirim dan menunggu persetujuan.');
            await loadSessionAndOrgs();
          }}>
            <input style={inputStyle} placeholder="Nama KSM / TPS3R / Bank Sampah" required value={orgName} onChange={(e) => setOrgName(e.target.value)} />
            <input style={inputStyle} placeholder="Kode singkat (huruf kecil & tanda hubung)" required value={orgSlug} onChange={(e) => setOrgSlug(e.target.value)} />
            <input style={inputStyle} placeholder="Kode Wilayah (mis. ID-YO-SLM)" required value={orgRegion} onChange={(e) => setOrgRegion(e.target.value)} />
            <button style={btnStyle} type="submit">Ajukan Organisasi</button>
          </form>
        </div>
      )}

      {tab === 'field' && (
        <div style={cardStyle}>
          {can('customer:manage') && (
            <>
              <h2 style={{ marginTop: 0 }}>Daftar Pelanggan</h2>
              {customers.length === 0 ? <p>Belum ada pelanggan terdaftar.</p> : (
                <ul>{customers.map((c) => <li key={c.id}>{c.display_name} ({c.phone_masked || 'tanpa nomor'})</li>)}</ul>
              )}
              <form onSubmit={async (e) => {
                e.preventDefault();
                await apiFetch(`/api/v1/organizations/${selectedOrg}/customers`, {
                  method: 'POST',
                  body: JSON.stringify({ display_name: custName, phone_masked: custPhone }),
                });
                setCustName('');
                setCustPhone('');
                setMsg('Pelanggan baru berhasil ditambahkan.');
                await loadOrgData(selectedOrg);
              }}>
                <h3>Tambah Pelanggan</h3>
                <input style={inputStyle} placeholder="Nama Pelanggan" required value={custName} onChange={(e) => setCustName(e.target.value)} />
                <input style={inputStyle} placeholder="No. HP (mis. 0812****8899)" value={custPhone} onChange={(e) => setCustPhone(e.target.value)} />
                <button style={btnStyle} type="submit">Simpan Pelanggan</button>
              </form>
              <hr style={{ margin: '18px 0', borderColor: '#e2e8f0' }} />
            </>
          )}
          {can('collection:execute') && (
            <>
              <h3>Penjemputan Lapangan (Mode Offline)</h3>
              <p>Antrean tersimpan di perangkat: <strong>{queue.length}</strong></p>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
                <button style={btnStyle} type="button" onClick={() => queueOfflinePickup('00000000-0000-0000-0000-000000000001', 'completed')}>
                  Selesai Jemput
                </button>
                <button style={{ ...btnStyle, background: '#b91c1c' }} type="button" onClick={() => queueOfflinePickup('00000000-0000-0000-0000-000000000001', 'failed_pickup', 'Gerbang terkunci')}>
                  Gagal Jemput
                </button>
                <button style={{ ...btnStyle, background: '#0369a1' }} type="button" onClick={syncOfflineQueue} disabled={queue.length === 0}>
                  Sinkronkan Sekarang ({queue.length})
                </button>
              </div>
            </>
          )}
        </div>
      )}

      {tab === 'finance' && (
        <div style={cardStyle}>
          <h2 style={{ marginTop: 0 }}>Tagihan &amp; Buku Besar</h2>
          {ledger && (
            <div style={{ background: '#f1f5f9', padding: 12, borderRadius: 8, marginBottom: 14 }}>
              <strong>Neraca Buku Besar:</strong> Selisih Rp {ledger.net_balance_idr.toLocaleString('id-ID')} ({ledger.net_balance_idr === 0 ? 'Seimbang' : 'Perlu Cek'}) ·
              Kas Masuk Rp {ledger.cash_idr.toLocaleString('id-ID')} ·
              Piutang Rp {ledger.receivable_idr.toLocaleString('id-ID')}
            </div>
          )}
          <h3>Daftar Tagihan</h3>
          {invoices.length === 0 ? <p>Belum ada tagihan diterbitkan.</p> : (
            <ul>
              {invoices.map((inv) => (
                <li key={inv.id}>Periode {inv.billing_period} — Rp {inv.amount_idr.toLocaleString('id-ID')} ({inv.status})</li>
              ))}
            </ul>
          )}
          {can('payment:verify') && <p><small>Aturan Maker-Checker aktif: verifikasi pembayaran hanya dapat dilakukan oleh petugas berbeda dari pencatat.</small></p>}
        </div>
      )}

      {tab === 'support' && (
        <div style={cardStyle}>
          {can('complaint:manage') && (
            <>
              <h2 style={{ marginTop: 0 }}>Pengaduan Warga</h2>
              {complaints.length === 0 ? <p>Tidak ada pengaduan saat ini.</p> : (
                <ul>
                  {complaints.map((c) => (
                    <li key={c.id}><strong>{c.title}</strong> — Status: {c.state} (Batas SLA: {new Date(c.due_at).toLocaleDateString('id-ID')})</li>
                  ))}
                </ul>
              )}
              <form onSubmit={async (e) => {
                e.preventDefault();
                await apiFetch(`/api/v1/organizations/${selectedOrg}/complaints`, {
                  method: 'POST',
                  body: JSON.stringify({ title: cmpTitle, description: cmpDesc }),
                });
                setCmpTitle('');
                setCmpDesc('');
                setMsg('Pengaduan warga berhasil dicatat.');
                await loadOrgData(selectedOrg);
              }}>
                <h3>Catat Pengaduan Baru</h3>
                <input style={inputStyle} placeholder="Judul Pengaduan" required value={cmpTitle} onChange={(e) => setCmpTitle(e.target.value)} />
                <input style={inputStyle} placeholder="Keterangan singkat" required value={cmpDesc} onChange={(e) => setCmpDesc(e.target.value)} />
                <button style={btnStyle} type="submit">Simpan Pengaduan</button>
              </form>
              <hr style={{ margin: '18px 0', borderColor: '#e2e8f0' }} />
            </>
          )}
          {can('report:export') && (
            <>
              <h3>Laporan Ringkas (Tanpa Data Pribadi)</h3>
              <button style={btnStyle} type="button" onClick={async () => {
                const r = await apiFetch<ReportSummary>(`/api/v1/organizations/${selectedOrg}/reports/summary?from=2026-11-01&to=2026-11-30`);
                setReport(r);
              }}>
                Muat Laporan Bulan Ini
              </button>
              {report && (
                <ul style={{ marginTop: 12 }}>
                  <li>Pelanggan Aktif: {report.customer_count}</li>
                  <li>Tugas Angkut: {report.collection_job_count}</li>
                  <li>Pengaduan Terbuka / Selesai: {report.open_complaints} / {report.resolved_complaints}</li>
                  <li>Total Tagihan: Rp {report.total_billed_idr.toLocaleString('id-ID')}</li>
                  <li>Pembayaran Ter-post: Rp {report.posted_payments_idr.toLocaleString('id-ID')}</li>
                </ul>
              )}
            </>
          )}
        </div>
      )}

      {tab === 'waste' && (
        <div style={cardStyle}>
          <h2 style={{ marginTop: 0 }}>TPS3R — Stok Material</h2>
          {inventory.length === 0 ? <p>Belum ada stok material tercatat.</p> : (
            <ul>
              {inventory.map((it) => (
                <li key={it.material_category}><strong>{it.material_category}</strong>: {it.available_kg} kg</li>
              ))}
            </ul>
          )}
          {can('waste:weigh') && (
            <form onSubmit={async (e) => {
              e.preventDefault();
              await apiFetch(`/api/v1/organizations/${selectedOrg}/waste/intake`, {
                method: 'POST',
                headers: { 'Idempotency-Key': newIdempotencyKey('intake') },
                body: JSON.stringify({
                  facility_name: 'TPS3R Utama',
                  material_category: intakeCat,
                  scale_id: 'SCALE-01',
                  weight_kg: Number(intakeKg),
                }),
              });
              setMsg('Timbangan masuk berhasil dicatat.');
              await loadOrgData(selectedOrg);
            }}>
              <h3>Catat Timbangan Masuk</h3>
              <input style={inputStyle} placeholder="Kategori Material (mis. PET)" required value={intakeCat} onChange={(e) => setIntakeCat(e.target.value)} />
              <input style={inputStyle} type="number" step="0.1" min="0.1" required value={intakeKg} onChange={(e) => setIntakeKg(e.target.value)} />
              <button style={btnStyle} type="submit">Catat Timbangan (kg)</button>
            </form>
          )}
        </div>
      )}

      {tab === 'tasks' && (
        <div style={cardStyle}>
          <h2 style={{ marginTop: 0 }}>Kotak Tugas Saya</h2>
          {tasks.length === 0 ? <p>Belum ada tugas untuk Anda saat ini.</p> : (
            <ul>
              {tasks.map((t) => (
                <li key={t.id}>[{t.priority === 'high' ? 'Prioritas Tinggi' : 'Normal'}] {t.business_type} — {t.state}</li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}
