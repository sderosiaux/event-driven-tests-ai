// Minimal vanilla SPA — four views, no build step. The control plane API is
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
  '/ui/evals': renderEvalRuns,
  '/ui/workers': renderWorkers,
};

// Run detail is dynamic — matched by prefix in route().
const RUN_DETAIL_PREFIX = '/ui/runs/';

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
  const runs = await fetchJSON('/api/v1/runs?limit=100');
  if (!runs.length) {
    app.innerHTML = `<div class="empty">
      <h2>No runs yet</h2>
      <p>Run <code>edt run --file scenario.yaml --report-to ${location.origin}</code>.</p>
    </div>`;
    return;
  }
  app.innerHTML = `<p class="muted" style="margin-bottom:12px">${runs.length} runs. Click any row for details.</p>
    <table>
    <thead><tr><th>Status</th><th>Scenario</th><th>Mode</th><th>Started</th><th>Duration</th><th>Run id</th></tr></thead>
    <tbody>
    ${runs.map(r => `<tr class="clickable" data-href="/ui/runs/${encodeURIComponent(r.id)}" style="cursor:pointer">
      <td class="status-${r.status}">${r.status.toUpperCase()}</td>
      <td><strong>${escape(r.scenario)}</strong></td>
      <td class="muted">${escape(r.mode)}</td>
      <td class="muted">${fmt(r.started_at)}</td>
      <td class="muted">${(r.duration_ns / 1e9).toFixed(2)}s</td>
      <td class="muted">${escape(r.id)}</td>
    </tr>`).join('')}
    </tbody>
  </table>`;
  // Row click → navigate to detail.
  app.querySelectorAll('tr.clickable').forEach(tr => {
    tr.addEventListener('click', () => {
      history.pushState(null, '', tr.dataset.href);
      route();
    });
  });
}

async function renderRunDetail(id) {
  const data = await fetchJSON('/api/v1/runs/' + encodeURIComponent(id));
  const run = data.run || {};
  const checks = data.checks || [];
  app.innerHTML = `
    <p><a href="/ui/runs">← Back to runs</a></p>
    <h2 style="margin:8px 0 16px">${escape(run.scenario)} <span class="status-${run.status}" style="font-size:12px;padding:3px 8px;border-radius:4px;background:var(--bg);border:1px solid var(--border)">${(run.status||'').toUpperCase()}</span></h2>
    <div style="display:grid;grid-template-columns:repeat(4,1fr);gap:16px;margin-bottom:20px">
      ${kvCard('Run ID', run.id)}
      ${kvCard('Mode', run.mode)}
      ${kvCard('Duration', run.duration_ns ? (run.duration_ns / 1e9).toFixed(3) + 's' : '—')}
      ${kvCard('Exit code', run.exit_code ?? 0)}
      ${kvCard('Started', fmt(run.started_at))}
      ${kvCard('Finished', fmt(run.finished_at))}
    </div>
    <h3 style="margin-top:24px">Checks (${checks.length})</h3>
    ${checks.length === 0 ? `<p class="muted">No checks recorded for this run.</p>` : `
    <table>
      <thead><tr><th>Name</th><th>Severity</th><th>Result</th><th>Value</th><th>Window</th><th>Error</th></tr></thead>
      <tbody>
      ${checks.map(c => `<tr>
        <td><strong>${escape(c.name)}</strong></td>
        <td class="muted">${escape(c.severity || 'warning')}</td>
        <td class="status-${c.passed ? 'pass' : 'fail'}">${c.passed ? 'PASS' : 'FAIL'}</td>
        <td class="muted"><code>${escape(String(c.value ?? '—'))}</code></td>
        <td class="muted">${escape(c.window || '—')}</td>
        <td class="muted">${escape(c.error || '—')}</td>
      </tr>`).join('')}
      </tbody>
    </table>`}
  `;
}

function kvCard(label, val) {
  return `<div style="background:var(--surface);border:1px solid var(--border);border-radius:6px;padding:10px 12px">
    <div class="muted" style="font-size:11px;text-transform:uppercase;letter-spacing:0.04em">${label}</div>
    <div style="margin-top:4px;font-family:'SF Mono',Consolas,monospace;font-size:12px">${escape(String(val ?? '—'))}</div>
  </div>`;
}

async function renderEvalRuns() {
  const runs = await fetchJSON('/api/v1/eval-runs?limit=50');
  if (!runs.length) {
    app.innerHTML = `<div class="empty">
      <h2>No eval runs yet</h2>
      <p>Run <code>edt eval --file scenario.yaml --report-to ${location.origin}</code> against a scenario that declares an <code>agent_under_test</code> block.</p>
    </div>`;
    return;
  }
  app.innerHTML = `<table>
    <thead><tr><th>Status</th><th>Scenario</th><th>Judge</th><th>Iters</th><th>Started</th><th>Run id</th></tr></thead>
    <tbody>
    ${runs.map(r => `<tr>
      <td class="status-${r.status}">${r.status.toUpperCase()}</td>
      <td>${escape(r.scenario)}</td>
      <td class="muted">${escape(r.judge_model || 'mixed')}</td>
      <td class="muted">${r.iterations}</td>
      <td class="muted">${fmt(r.started_at)}</td>
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
  try {
    if (path.startsWith(RUN_DETAIL_PREFIX) && path.length > RUN_DETAIL_PREFIX.length) {
      const id = decodeURIComponent(path.slice(RUN_DETAIL_PREFIX.length));
      await renderRunDetail(id);
      return;
    }
    const handler = routes[path] || routes['/'];
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
  const isKnown = routes[a.pathname] || a.pathname.startsWith(RUN_DETAIL_PREFIX);
  if (!isKnown) return;
  e.preventDefault();
  history.pushState(null, '', a.pathname);
  route();
});
route();
refreshHealth();
setInterval(refreshHealth, 10000);
