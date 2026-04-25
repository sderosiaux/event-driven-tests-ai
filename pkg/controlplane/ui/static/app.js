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

// Dynamic-prefix routes matched in route() below.
const RUN_DETAIL_PREFIX = '/ui/runs/';
const EVAL_DETAIL_PREFIX = '/ui/evals/';

async function renderScenarios() {
  const ss = await fetchJSON('/api/v1/scenarios');
  if (!ss.length) {
    app.innerHTML = `<div class="empty">
      <h2>No scenarios yet</h2>
      <p>Open the <a href="/ui/builder">Builder</a> to create one visually, or POST a YAML to <code>/api/v1/scenarios</code>.</p>
    </div>`;
    return;
  }
  // Pull recent runs once and bucket by scenario so each row can show its
  // last verdict + timestamp without N+1 fetches.
  let lastRunByScenario = {};
  try {
    const runs = await fetchJSON('/api/v1/runs?limit=200');
    for (const r of runs) {
      if (!lastRunByScenario[r.scenario]) lastRunByScenario[r.scenario] = r;
    }
  } catch (e) { /* runs API down — render scenarios without last-run column */ }

  app.innerHTML = `<div class="list-intro"><span class="count-chip">${ss.length}</span> scenarios — click any row to open it in the Builder.</div>
    <table>
    <thead><tr><th>Name</th><th>Version</th><th>Labels</th><th>Last run</th><th>Updated</th></tr></thead>
    <tbody>
    ${ss.map(s => {
      const r = lastRunByScenario[s.name];
      const lastCell = r
        ? `<a class="last-run-link" href="/ui/runs/${encodeURIComponent(r.id)}" data-stop>
             <span class="status-pill status-${escape(r.status)}">${escape(String(r.status || '').toUpperCase())}</span>
             <span class="muted">${fmt(r.started_at)}</span>
           </a>`
        : `<span class="muted">—</span>`;
      const evals = s.parsed?.spec?.evals || [];
      const stepCount = (s.parsed?.spec?.steps || []).length;
      const checkCount = (s.parsed?.spec?.checks || []).length;
      const flags = `
        <span class="kind-chip">${stepCount} steps</span>
        ${checkCount ? `<span class="kind-chip">${checkCount} checks</span>` : ''}
        ${evals.length ? `<span class="kind-chip kind-eval">eval × ${evals.length}</span>` : ''}
      `;
      return `<tr class="clickable" data-scenario="${escape(s.name)}">
        <td><strong>${escape(s.name)}</strong><div class="kind-chip-row">${flags}</div></td>
        <td>v${s.version}</td>
        <td class="muted">${labels(s.labels)}</td>
        <td>${lastCell}</td>
        <td class="muted">${fmt(s.updated_at)}</td>
      </tr>`;
    }).join('')}
    </tbody>
  </table>`;
  // Click row → builder. The last-run link is a separate <a>; clicks on it
  // (or anything inside [data-stop]) shouldn't bubble up to the row handler.
  app.querySelectorAll('tr.clickable').forEach(tr => {
    tr.addEventListener('click', (e) => {
      if (e.target.closest('[data-stop]')) return;
      location.href = '/ui/builder?scenario=' + encodeURIComponent(tr.dataset.scenario);
    });
  });
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
  app.innerHTML = `<div class="list-intro"><span class="count-chip">${runs.length}</span> runs — click any row for details.</div>
    <table>
    <thead><tr><th>Status</th><th>Scenario</th><th>Mode</th><th>Started</th><th>Duration</th><th>Run id</th></tr></thead>
    <tbody>
    ${runs.map(r => `<tr class="clickable" data-href="/ui/runs/${encodeURIComponent(r.id)}">
      <td><span class="status-pill status-${escape(r.status)}">${escape(String(r.status || '').toUpperCase())}</span></td>
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
  // Sanitise status for BOTH the HTML attribute and the text context so a
  // backend shipping an unexpected value can't break the DOM (codex P1).
  const safeStatus = escape(run.status || '');
  app.innerHTML = `
    <a href="/ui/runs" class="back-link">← Back to runs</a>
    <div class="run-header status-${safeStatus}">
      <h2><a class="scenario-link" href="/ui/builder?scenario=${encodeURIComponent(run.scenario || '')}">${escape(run.scenario)}</a></h2>
      <span class="verdict-pill">${safeStatus.toUpperCase()}</span>
      <span class="run-header-meta">${fmt(run.started_at)} · ${run.duration_ns ? (run.duration_ns / 1e9).toFixed(3) + 's' : '—'}</span>
    </div>
    <div class="kv-grid">
      ${kvCard('Run ID', run.id)}
      ${kvCard('Mode', run.mode)}
      ${kvCard('Duration', run.duration_ns ? (run.duration_ns / 1e9).toFixed(3) + 's' : '—')}
      ${kvCard('Exit code', run.exit_code ?? 0)}
      ${kvCard('Started', fmt(run.started_at))}
      ${kvCard('Finished', fmt(run.finished_at))}
    </div>
    ${(data.steps && data.steps.length) ? `
      <h3 class="section-label">Execution outline <span class="count">${data.steps.length}</span></h3>
      <ol class="step-list">
        ${data.steps.map((s, i) => `
          <li class="step-row step-${escape(s.kind || 'unknown')}">
            <span class="step-pip">${i + 1}</span>
            <span class="step-kind">${escape(s.kind || '')}</span>
            <span class="step-name">${escape(s.name || '')}</span>
            <span class="step-where mono">${escape(s.where || '')}</span>
            ${s.meta ? `<span class="step-meta mono">${escape(s.meta)}</span>` : ''}
          </li>
        `).join('')}
      </ol>
    ` : ''}
    <h3 class="section-label">Checks <span class="count">${checks.length}</span></h3>
    ${checks.length === 0 ? `<p class="muted">No checks recorded for this run.</p>` : `
    <table>
      <thead><tr><th>Name</th><th>Severity</th><th>Result</th><th>Value</th><th>Window</th><th>Error</th></tr></thead>
      <tbody>
      ${checks.map(c => { const sev = c.severity || 'warning'; return `<tr>
        <td><strong>${escape(c.name)}</strong></td>
        <td><span class="sev-badge sev-${escape(sev)}">${escape(sev)}</span></td>
        <td><span class="status-pill status-${c.passed ? 'pass' : 'fail'}">${c.passed ? 'PASS' : 'FAIL'}</span></td>
        <td class="muted"><code>${escape(String(c.value ?? '—'))}</code></td>
        <td class="muted">${escape(c.window || '—')}</td>
        <td class="muted">${escape(c.error || '—')}</td>
      </tr>`; }).join('')}
      </tbody>
    </table>`}
  `;
}

function kvCard(label, val) {
  return `<div class="kv-card">
    <div class="kv-label">${label}</div>
    <div class="kv-value">${escape(String(val ?? '—'))}</div>
  </div>`;
}

// Render the observed-value block for one eval. Parses thresholds like
// ">= 4.2" / "< 0.2" / "0.8" so we can show the gap (delta from threshold)
// and a small red/green directional cue. Falls back to just the number
// when the threshold isn't a recognisable comparison.
function renderThresholdGap(value, thresholdStr, passed) {
  if (typeof value !== 'number') {
    return `<div class="kv-label">Observed</div><div class="threshold-row"><strong class="big-number">—</strong></div>`;
  }
  const m = String(thresholdStr || '').match(/(>=|<=|>|<|==|=)\s*(-?\d+(?:\.\d+)?)/);
  let gapHtml = '';
  if (m) {
    const op = m[1];
    const target = parseFloat(m[2]);
    const delta = value - target;
    const sign = delta >= 0 ? '+' : '';
    const dir = (op.startsWith('>') && delta < 0) || (op.startsWith('<') && delta > 0) ? 'below' : 'above';
    gapHtml = `<span class="gap-delta ${passed ? 'gap-ok' : 'gap-bad'}">${sign}${delta.toFixed(2)}</span> <span class="muted">${dir} threshold ${escape(op + ' ' + target)}</span>`;
  } else {
    gapHtml = `<span class="muted">threshold ${escape(thresholdStr || '—')}</span>`;
  }
  return `
    <div class="kv-label">Observed</div>
    <div class="threshold-row">
      <strong class="big-number">${value.toFixed(2)}</strong>
      ${gapHtml}
    </div>
  `;
}

// Collapsible block of per-iteration transcripts (input/output/score+reason).
// The summary line shows score distribution at a glance; expanding reveals
// the full pairs so the eval result is debuggable instead of opaque.
function renderTranscriptBlock(blockId, transcripts) {
  const failed = transcripts.filter(t => Number(t.score) < 4).length;
  const passed = transcripts.length - failed;
  const summary = `${transcripts.length} transcripts · ${passed} ≥4 · ${failed} <4`;
  return `
    <details class="transcripts" id="${blockId}">
      <summary>
        <span class="transcripts-label">Per-iteration transcripts</span>
        <span class="transcripts-meta">${summary}</span>
        <span class="transcripts-chev">▾</span>
      </summary>
      <div class="transcripts-body">
        ${transcripts.map(t => {
          const scoreNum = Number(t.score);
          const scoreCls = scoreNum >= 4 ? 'good' : scoreNum >= 3 ? 'mid' : 'bad';
          return `
            <article class="transcript">
              <header class="transcript-head">
                <span class="transcript-iter">#${(t.iteration ?? 0) + 1}</span>
                <span class="transcript-score score-${scoreCls}">${scoreNum.toFixed(1)}</span>
                <span class="transcript-judge mono">${escape(t.judge_model || '')}</span>
                ${t.latency_ms != null ? `<span class="transcript-latency mono">${t.latency_ms}ms</span>` : ''}
              </header>
              <div class="transcript-grid">
                <div>
                  <div class="kv-label">INPUT</div>
                  <pre class="transcript-text">${escape(t.input || '')}</pre>
                </div>
                <div>
                  <div class="kv-label">AGENT OUTPUT</div>
                  <pre class="transcript-text">${escape(t.output || '')}</pre>
                </div>
                <div class="transcript-reasoning">
                  <div class="kv-label">JUDGE REASONING</div>
                  <p>${escape(t.reasoning || '')}</p>
                </div>
              </div>
            </article>
          `;
        }).join('')}
      </div>
    </details>
  `;
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
  app.innerHTML = `<div class="list-intro"><span class="count-chip">${runs.length}</span> eval runs — click any row for per-eval scores.</div>
    <table>
    <thead><tr><th>Status</th><th>Scenario</th><th>Judge</th><th>Iters</th><th>Started</th><th>Run id</th></tr></thead>
    <tbody>
    ${runs.map(r => `<tr class="clickable" data-href="/ui/evals/${encodeURIComponent(r.id)}">
      <td><span class="status-pill status-${escape(r.status)}">${escape(String(r.status || '').toUpperCase())}</span></td>
      <td><strong>${escape(r.scenario)}</strong></td>
      <td class="muted">${escape(r.judge_model || 'mixed')}</td>
      <td class="muted">${r.iterations}</td>
      <td class="muted">${fmt(r.started_at)}</td>
      <td class="muted">${escape(r.id)}</td>
    </tr>`).join('')}
    </tbody>
  </table>`;
  app.querySelectorAll('tr.clickable').forEach(tr => {
    tr.addEventListener('click', () => {
      history.pushState(null, '', tr.dataset.href);
      route();
    });
  });
}

async function renderEvalRunDetail(id) {
  const data = await fetchJSON('/api/v1/eval-runs/' + encodeURIComponent(id));
  const run = data.run || {};
  const results = data.results || [];
  const aut = data.agent_under_test || null;
  const evalDefs = data.evals || [];
  // Index eval definitions by name so each result row can pull its own
  // rubric / judge / threshold without rescanning.
  const evalByName = Object.fromEntries(evalDefs.map(e => [e.name, e]));
  const safeStatus = escape(run.status || '');

  const autBlock = aut ? `
    <h3 class="section-label">Agent under test</h3>
    <div class="kv-grid kv-grid-3">
      ${kvCard('Name', aut.name)}
      ${kvCard('Consumes', (aut.consumes || []).join(', ') || '—')}
      ${kvCard('Produces', (aut.produces || []).join(', ') || '—')}
    </div>
  ` : '';

  const evalCards = evalDefs.length === 0 ? '' : `
    <h3 class="section-label">Eval criteria <span class="count">${evalDefs.length}</span></h3>
    <div class="eval-defs">
      ${evalDefs.map((ev, idx) => {
        const judge = ev.judge || {};
        const thr = ev.threshold || {};
        const sev = ev.severity || 'warning';
        const result = results.find(r => r.name === ev.name);
        const passed = result ? result.passed : null;
        const verdictCls = passed === true ? 'status-pass' : passed === false ? 'status-fail' : '';
        const transcripts = result?.transcripts || [];
        return `
          <article class="eval-def ${verdictCls}">
            <header class="eval-def-head">
              <span class="eval-def-name">${escape(ev.name)}</span>
              <span class="sev-badge sev-${escape(sev)}">${escape(sev)}</span>
              ${result ? `<span class="status-pill status-${escape(passed ? 'pass' : 'fail')}">${passed ? 'PASS' : 'FAIL'}</span>` : ''}
              <span class="eval-def-meta">judge <code>${escape(judge.model || '—')}</code>${judge.rubric_version ? ' · rubric ' + escape(judge.rubric_version) : ''}</span>
            </header>
            <div class="eval-def-body">
              <div class="eval-def-col">
                <div class="kv-label">Rubric prompt</div>
                <pre class="rubric-prompt">${escape(judge.rubric || '(no rubric defined)')}</pre>
              </div>
              <div class="eval-def-col eval-def-col-right">
                <div class="kv-label">Threshold</div>
                <div class="threshold-row"><code>${escape(thr.aggregate || 'avg')}</code> <code>${escape(thr.value || '—')}</code> over <code>${escape(thr.over || '—')}</code></div>
                ${result ? renderThresholdGap(result.value, thr.value, passed) : '<div class="muted" style="font-size:12px">No samples recorded for this eval.</div>'}
              </div>
            </div>
            ${transcripts.length > 0 ? renderTranscriptBlock(`tr-${idx}`, transcripts) : ''}
          </article>
        `;
      }).join('')}
    </div>
  `;

  const resultRows = results.length === 0 ? '' : `
    <h3 class="section-label">Per-eval scores <span class="count">${results.length}</span></h3>
    <table>
      <thead><tr><th>Name</th><th>Aggregate</th><th>Result</th><th>Value</th><th>Threshold</th><th>Samples</th><th>Errors</th></tr></thead>
      <tbody>
      ${results.map(r => `<tr>
        <td><strong>${escape(r.name)}</strong>${evalByName[r.name]?.severity ? ` <span class="sev-badge sev-${escape(evalByName[r.name].severity)}">${escape(evalByName[r.name].severity)}</span>` : ''}</td>
        <td class="muted">${escape(r.aggregate || 'mean')}</td>
        <td><span class="status-pill status-${escape(r.passed ? 'pass' : 'fail')}">${r.passed ? 'PASS' : 'FAIL'}</span></td>
        <td class="muted"><code>${escape(typeof r.value === 'number' ? r.value.toFixed(3) : String(r.value ?? '—'))}</code></td>
        <td class="muted"><code>${escape(r.threshold || '—')}</code></td>
        <td class="muted">${r.samples}/${r.required_samples}</td>
        <td class="muted">${r.errors || 0}</td>
      </tr>`).join('')}
      </tbody>
    </table>`;

  app.innerHTML = `
    <a href="/ui/evals" class="back-link">← Back to evals</a>
    <div class="run-header status-${safeStatus}">
      <h2><a class="scenario-link" href="/ui/builder?scenario=${encodeURIComponent(run.scenario || '')}">${escape(run.scenario)}</a></h2>
      <span class="verdict-pill">${safeStatus.toUpperCase()}</span>
      <span class="run-header-meta">${fmt(run.started_at)} · ${run.iterations || 0} iterations · judge ${escape(run.judge_model || 'mixed')}</span>
    </div>
    <div class="kv-grid">
      ${kvCard('Eval Run ID', run.id)}
      ${kvCard('Scenario', run.scenario)}
      ${kvCard('Judge', run.judge_model)}
      ${kvCard('Iterations', run.iterations)}
      ${kvCard('Started', fmt(run.started_at))}
      ${kvCard('Finished', fmt(run.finished_at))}
    </div>
    ${autBlock}
    ${evalCards}
    ${resultRows}
    ${evalDefs.length === 0 && results.length === 0 ? '<p class="muted">No eval criteria defined and no scores recorded for this run.</p>' : ''}
  `;
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
  if (!o) return '<span class="muted">—</span>';
  const entries = Object.entries(o);
  if (entries.length === 0) return '<span class="muted">—</span>';
  return entries.map(([k, v]) =>
    `<span class="label-chip"><span class="label-k">${escape(k)}</span><span class="label-v">${escape(String(v))}</span></span>`
  ).join('');
}

function escape(s) {
  return String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

function syncActiveNav() {
  // Mark the top nav link matching the current path as .active so the
  // builder/runs/etc sections get a persistent visual anchor without each
  // page having to hard-code the class.
  const path = location.pathname;
  document.querySelectorAll('header nav a').forEach(a => {
    const target = new URL(a.href, location.origin).pathname;
    const match = target === path || (target !== '/' && path.startsWith(target));
    a.classList.toggle('active', match);
  });
}

async function route() {
  const path = location.pathname;
  syncActiveNav();
  try {
    if (path.startsWith(RUN_DETAIL_PREFIX) && path.length > RUN_DETAIL_PREFIX.length) {
      await renderRunDetail(decodeURIComponent(path.slice(RUN_DETAIL_PREFIX.length)));
      return;
    }
    if (path.startsWith(EVAL_DETAIL_PREFIX) && path.length > EVAL_DETAIL_PREFIX.length) {
      await renderEvalRunDetail(decodeURIComponent(path.slice(EVAL_DETAIL_PREFIX.length)));
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
  const isKnown = routes[a.pathname] || a.pathname.startsWith(RUN_DETAIL_PREFIX) || a.pathname.startsWith(EVAL_DETAIL_PREFIX);
  if (!isKnown) return;
  e.preventDefault();
  history.pushState(null, '', a.pathname);
  route();
});
route();
refreshHealth();
setInterval(refreshHealth, 10000);
