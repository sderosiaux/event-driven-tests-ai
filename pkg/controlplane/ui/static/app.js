// Minimal vanilla SPA — three views, no build step. The control plane API is
// the source of truth; this file is a thin renderer.

const app = document.getElementById('app');
const healthEl = document.getElementById('health');

async function fetchJSON(url) {
  const r = await fetch(url, { headers: { Accept: 'application/json' } });
  if (!r.ok) throw new Error(`${url}: ${r.status}`);
  return r.json();
}

function fmt(d) {
  try { return new Date(d).toLocaleString(); } catch { return d; }
}

async function refreshHealth() {
  try {
    await fetchJSON('/healthz');
    healthEl.textContent = 'control plane up';
    healthEl.style.color = 'var(--pass)';
  } catch (e) {
    healthEl.textContent = 'control plane down';
    healthEl.style.color = 'var(--fail)';
  }
}

const routes = {
  '/': renderScenarios,
  '/ui/runs': renderRuns,
  '/ui/workers': renderWorkers,
};

async function renderScenarios() {
  const ss = await fetchJSON('/api/v1/scenarios');
  if (!ss.length) {
    app.innerHTML = `<div class="empty">
      <h2>No scenarios yet</h2>
      <p>POST a YAML scenario to <code>/api/v1/scenarios</code> to get started.</p>
    </div>`;
    return;
  }
  app.innerHTML = `<table>
    <thead><tr><th>Name</th><th>Version</th><th>Labels</th><th>Updated</th></tr></thead>
    <tbody>
    ${ss.map(s => `<tr>
      <td><strong>${escape(s.name)}</strong></td>
      <td>v${s.version}</td>
      <td class="muted">${labels(s.labels)}</td>
      <td class="muted">${fmt(s.updated_at)}</td>
    </tr>`).join('')}
    </tbody>
  </table>`;
}

async function renderRuns() {
  const runs = await fetchJSON('/api/v1/runs?limit=50');
  if (!runs.length) {
    app.innerHTML = `<div class="empty">
      <h2>No runs yet</h2>
      <p>Run <code>edt run --file scenario.yaml --report-to ${location.origin}</code>.</p>
    </div>`;
    return;
  }
  app.innerHTML = `<table>
    <thead><tr><th>Status</th><th>Scenario</th><th>Mode</th><th>Started</th><th>Duration</th><th>Run id</th></tr></thead>
    <tbody>
    ${runs.map(r => `<tr>
      <td class="status-${r.status}">${r.status.toUpperCase()}</td>
      <td>${escape(r.scenario)}</td>
      <td class="muted">${escape(r.mode)}</td>
      <td class="muted">${fmt(r.started_at)}</td>
      <td class="muted">${(r.duration_ns / 1e9).toFixed(2)}s</td>
      <td class="muted">${escape(r.id)}</td>
    </tr>`).join('')}
    </tbody>
  </table>`;
}

async function renderWorkers() {
  const ws = await fetchJSON('/api/v1/workers');
  if (!ws.length) {
    app.innerHTML = `<div class="empty">
      <h2>No workers registered</h2>
      <p>Run <code>edt worker --control-plane ${location.origin}</code>.</p>
    </div>`;
    return;
  }
  app.innerHTML = `<table>
    <thead><tr><th>Worker id</th><th>Labels</th><th>Version</th><th>Last heartbeat</th></tr></thead>
    <tbody>
    ${ws.map(w => `<tr>
      <td>${escape(w.id)}</td>
      <td class="muted">${labels(w.labels)}</td>
      <td class="muted">${escape(w.version || '—')}</td>
      <td class="muted">${fmt(w.last_heartbeat)}</td>
    </tr>`).join('')}
    </tbody>
  </table>`;
}

function labels(o) {
  if (!o) return '—';
  return Object.entries(o).map(([k, v]) => `${k}=${v}`).join(', ');
}

function escape(s) {
  return String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

async function route() {
  const path = location.pathname;
  const handler = routes[path] || routes['/'];
  try {
    await handler();
  } catch (e) {
    app.innerHTML = `<div class="empty"><h2>Error</h2><p>${escape(e.message)}</p></div>`;
  }
}

window.addEventListener('popstate', route);
document.addEventListener('click', e => {
  const a = e.target.closest('a');
  if (!a || a.target === '_blank') return;
  if (!a.href.startsWith(location.origin)) return;
  if (!routes[a.pathname]) return;
  e.preventDefault();
  history.pushState(null, '', a.pathname);
  route();
});
route();
refreshHealth();
setInterval(refreshHealth, 10000);
