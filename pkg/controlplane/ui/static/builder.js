// Scenario builder — vanilla JS + SVG. No framework, no build step.
//
// State is a single object; every mutation re-renders the canvas and the
// live YAML preview. Drag-to-reorder swaps steps[] indices. Save posts the
// emitted YAML to /api/v1/scenarios and surfaces the control plane's reply
// as a toast.

// ----- State ---------------------------------------------------------------

const DEFAULT_STATE = {
  name: 'my-scenario',
  labels: {},
  connectors: {
    kafka: { bootstrap_servers: '' },
    http: { base_url: '' },
    websocket: { base_url: '' },
    grpc: { address: '' },
  },
  steps: [],
  checks: [],
};

const state = JSON.parse(JSON.stringify(DEFAULT_STATE));
let stepSeq = 0;
const nextId = () => `s${++stepSeq}`;

// ----- Step templates ------------------------------------------------------

const TEMPLATES = {
  produce: () => ({ id: nextId(), type: 'produce', name: 'place-orders', topic: 'orders', payload: '${data.orders}', count: 10, rate: '' }),
  consume: () => ({ id: nextId(), type: 'consume', name: 'wait-ack', topic: 'orders.ack', group: 'edt-run', timeout: '5s', match: '' }),
  http:    () => ({ id: nextId(), type: 'http', name: 'call-api', method: 'GET', path: '/health', body: '', expectStatus: 200 }),
  websocket:() => ({ id: nextId(), type: 'websocket', name: 'watch-stream', path: '/orders/stream', send: '', count: 0, timeout: '30s', match: '' }),
  sse:      () => ({ id: nextId(), type: 'sse', name: 'watch-events', path: '/events', count: 0, timeout: '30s', match: '' }),
  grpc:     () => ({ id: nextId(), type: 'grpc', name: 'rpc-call', proto: 'syntax = "proto3";\nmessage Empty {}\nservice S { rpc M(Empty) returns (Empty); }', method: 'pkg.S/M', request: '{}', expectCode: 0 }),
  sleep:    () => ({ id: nextId(), type: 'sleep', name: 'pause', duration: '1s' }),
};

// ----- YAML emitter --------------------------------------------------------
// Handwritten because we can't pull in npm packages. Handles the subset of
// YAML the scenario shape actually uses: maps, arrays, scalars, block scalars
// for multi-line strings (protos, CEL).

function yamlEscape(s) {
  s = String(s);
  if (s === '') return '""';
  if (/^(true|false|null|~|\d+(\.\d+)?)$/i.test(s)) return `"${s}"`;
  if (/[:#@`\[\]{}|>*&!%,]|^-|^\?/.test(s) || s.includes('\n')) {
    if (s.includes('\n')) {
      // Block scalar.
      return '|-\n' + s.split('\n').map(l => '  ' + l).join('\n');
    }
    return JSON.stringify(s);
  }
  return s;
}

function emitValue(v, indent) {
  const pad = '  '.repeat(indent);
  if (v === null || v === undefined) return 'null';
  if (typeof v === 'boolean' || typeof v === 'number') return String(v);
  if (typeof v === 'string') {
    if (v.includes('\n')) {
      return '|-\n' + v.split('\n').map(l => pad + '  ' + l).join('\n');
    }
    return yamlEscape(v);
  }
  if (Array.isArray(v)) {
    if (v.length === 0) return '[]';
    return '\n' + v.map(item => {
      const rendered = emitValue(item, indent + 1);
      if (rendered.startsWith('\n')) {
        // Nested map — inline the first line with "- ", indent the rest.
        const lines = rendered.slice(1).split('\n');
        return pad + '- ' + lines[0].slice(pad.length + 2) + '\n' + lines.slice(1).join('\n');
      }
      return pad + '- ' + rendered;
    }).join('\n');
  }
  if (typeof v === 'object') {
    const keys = Object.keys(v);
    if (keys.length === 0) return '{}';
    return '\n' + keys.map(k => {
      const rendered = emitValue(v[k], indent + 1);
      if (rendered.startsWith('\n')) return pad + k + ':' + rendered;
      return pad + k + ': ' + rendered;
    }).join('\n');
  }
  return '';
}

function emitYAML(s) {
  const clean = toWireShape(s);
  const body = emitValue(clean, 0);
  return body.startsWith('\n') ? body.slice(1) : body;
}

// Convert builder state → canonical scenario wire shape, dropping empty
// fields so the YAML stays readable.
function toWireShape(s) {
  const out = {
    apiVersion: 'edt.io/v1',
    kind: 'Scenario',
    metadata: { name: s.name || 'unnamed' },
    spec: { connectors: {}, steps: [] },
  };
  if (Object.keys(s.labels).length) out.metadata.labels = s.labels;

  if (s.connectors.kafka.bootstrap_servers) out.spec.connectors.kafka = { bootstrap_servers: s.connectors.kafka.bootstrap_servers };
  if (s.connectors.http.base_url)           out.spec.connectors.http  = { base_url: s.connectors.http.base_url };
  if (s.connectors.websocket.base_url)      out.spec.connectors.websocket = { base_url: s.connectors.websocket.base_url };
  if (s.connectors.grpc.address)            out.spec.connectors.grpc  = { address: s.connectors.grpc.address };

  out.spec.steps = s.steps.map(stepToWire);
  if (s.checks.length) out.spec.checks = s.checks.map(c => ({ name: c.name, expr: c.expr, ...(c.severity ? { severity: c.severity } : {}) }));
  return out;
}

function stepToWire(st) {
  switch (st.type) {
    case 'produce':
      return { name: st.name, produce: dropEmpty({ topic: st.topic, payload: st.payload, count: num(st.count), rate: st.rate }) };
    case 'consume':
      return { name: st.name, consume: dropEmpty({ topic: st.topic, group: st.group, timeout: st.timeout, match: matchArr(st.match) }) };
    case 'http':
      return { name: st.name, http: dropEmpty({ method: st.method, path: st.path, body: st.body, expect: st.expectStatus ? { status: num(st.expectStatus) } : undefined }) };
    case 'websocket':
      return { name: st.name, websocket: dropEmpty({ path: st.path, send: st.send, count: num(st.count), timeout: st.timeout, match: matchArr(st.match) }) };
    case 'sse':
      return { name: st.name, sse: dropEmpty({ path: st.path, count: num(st.count), timeout: st.timeout, match: matchArr(st.match) }) };
    case 'grpc':
      return { name: st.name, grpc: dropEmpty({ proto: st.proto, method: st.method, request: st.request, expect: st.expectCode !== undefined && st.expectCode !== '' ? { code: num(st.expectCode) } : undefined }) };
    case 'sleep':
      return { name: st.name, sleep: st.duration };
  }
}

function matchArr(raw) {
  if (!raw || !raw.trim()) return undefined;
  return raw.split('\n').map(l => l.trim()).filter(Boolean).map(key => ({ key }));
}
function num(v) {
  if (v === '' || v === null || v === undefined) return undefined;
  const n = Number(v);
  if (Number.isFinite(n) && n !== 0) return n;
  if (n === 0) return undefined; // zero is "unset" for our count/status fields
  return undefined;
}
function dropEmpty(o) {
  for (const k of Object.keys(o)) {
    const v = o[k];
    if (v === '' || v === undefined || v === null) delete o[k];
  }
  return o;
}

// ----- Rendering -----------------------------------------------------------

const canvas = document.getElementById('canvas');
const yamlOut = document.getElementById('yaml-out');
const toast = document.getElementById('toast');

function render() {
  renderCanvas();
  yamlOut.textContent = emitYAML(state);
  syncInputs();
}

function renderCanvas() {
  if (state.steps.length === 0 && state.checks.length === 0) {
    canvas.innerHTML = `<div class="canvas-empty">
      <h3>Empty scenario</h3>
      <p>Add a step from the left palette to start. Cards render top-to-bottom as the execution order.</p>
    </div>`;
    return;
  }
  canvas.innerHTML = '';
  state.steps.forEach((step, idx) => canvas.appendChild(cardForStep(step, idx)));
  state.checks.forEach((check, idx) => canvas.appendChild(cardForCheck(check, idx)));
}

function cardForStep(step, idx) {
  const el = document.createElement('article');
  el.className = `card type-${step.type}`;
  el.dataset.id = step.id;
  el.dataset.kind = 'step';
  el.draggable = true;
  el.innerHTML = `
    <header draggable-handle>
      <span class="type-badge">${step.type}</span>
      <input class="title" data-field="name" value="${escapeHTML(step.name)}" placeholder="step name">
      <button class="remove" title="Remove step">×</button>
    </header>
    <div class="body">${bodyForStep(step)}</div>
  `;
  el.querySelector('.remove').addEventListener('click', () => {
    state.steps = state.steps.filter(s => s.id !== step.id);
    render();
  });
  wireFields(el, step, idx, 'step');
  wireDrag(el, 'step');
  return el;
}

function cardForCheck(check, idx) {
  const el = document.createElement('article');
  el.className = `card type-check`;
  el.dataset.id = check.id;
  el.dataset.kind = 'check';
  el.innerHTML = `
    <header>
      <span class="type-badge">check</span>
      <input class="title" data-field="name" value="${escapeHTML(check.name)}" placeholder="check name">
      <button class="remove" title="Remove check">×</button>
    </header>
    <div class="body">
      <div class="field">
        <label>CEL expression</label>
        <textarea data-field="expr" rows="2" placeholder="size(stream('orders')) >= 10">${escapeHTML(check.expr)}</textarea>
      </div>
      <div class="field">
        <label>Severity</label>
        <select data-field="severity">
          <option value="critical" ${check.severity==='critical'?'selected':''}>critical</option>
          <option value="warning"  ${check.severity==='warning'?'selected':''}>warning</option>
          <option value="info"     ${check.severity==='info'?'selected':''}>info</option>
        </select>
      </div>
    </div>
  `;
  el.querySelector('.remove').addEventListener('click', () => {
    state.checks = state.checks.filter(c => c.id !== check.id);
    render();
  });
  wireFields(el, check, idx, 'check');
  return el;
}

function bodyForStep(s) {
  switch (s.type) {
    case 'produce': return (
      twoCol(
        field('Topic',   `<input data-field="topic"   value="${escapeHTML(s.topic)}">`),
        field('Count',   `<input data-field="count"   value="${s.count??''}" type="number">`),
      ) +
      field('Payload (CEL / ${data.*} / JSON)', `<textarea data-field="payload" rows="2">${escapeHTML(s.payload)}</textarea>`) +
      field('Rate (optional, e.g. 50/s)', `<input data-field="rate" value="${escapeHTML(s.rate||'')}">`)
    );
    case 'consume': return (
      twoCol(
        field('Topic',   `<input data-field="topic"   value="${escapeHTML(s.topic)}">`),
        field('Group',   `<input data-field="group"   value="${escapeHTML(s.group)}">`),
      ) +
      twoCol(
        field('Timeout', `<input data-field="timeout" value="${escapeHTML(s.timeout)}">`),
        ''
      ) +
      field('Match rules (CEL, one per line)', `<textarea data-field="match" rows="2" placeholder="payload.orderId == previous.orderId">${escapeHTML(s.match||'')}</textarea>`)
    );
    case 'http': return (
      twoCol(
        field('Method', `<select data-field="method">${['GET','POST','PUT','PATCH','DELETE'].map(m => `<option ${m===s.method?'selected':''}>${m}</option>`).join('')}</select>`),
        field('Path',   `<input data-field="path"   value="${escapeHTML(s.path)}">`),
      ) +
      field('Body (JSON, optional)', `<textarea data-field="body" rows="2">${escapeHTML(s.body||'')}</textarea>`) +
      field('Expect status', `<input data-field="expectStatus" value="${s.expectStatus??''}" type="number">`)
    );
    case 'websocket': return (
      twoCol(
        field('Path',    `<input data-field="path"    value="${escapeHTML(s.path)}">`),
        field('Timeout', `<input data-field="timeout" value="${escapeHTML(s.timeout)}">`),
      ) +
      field('Send (optional JSON, interpolated)', `<textarea data-field="send" rows="2">${escapeHTML(s.send||'')}</textarea>`) +
      twoCol(
        field('Count (0 = first-match-or-timeout)', `<input data-field="count" value="${s.count??''}" type="number">`),
        ''
      ) +
      field('Match rules (CEL, one per line)', `<textarea data-field="match" rows="2">${escapeHTML(s.match||'')}</textarea>`)
    );
    case 'sse': return (
      twoCol(
        field('Path',    `<input data-field="path"    value="${escapeHTML(s.path)}">`),
        field('Timeout', `<input data-field="timeout" value="${escapeHTML(s.timeout)}">`),
      ) +
      twoCol(
        field('Count', `<input data-field="count" value="${s.count??''}" type="number">`),
        ''
      ) +
      field('Match rules (CEL, one per line)', `<textarea data-field="match" rows="2">${escapeHTML(s.match||'')}</textarea>`)
    );
    case 'grpc': return (
      twoCol(
        field('Method (pkg.Svc/Method)', `<input data-field="method" value="${escapeHTML(s.method)}">`),
        field('Expect code', `<input data-field="expectCode" value="${s.expectCode??''}" type="number">`),
      ) +
      field('Inline .proto', `<textarea data-field="proto" rows="4">${escapeHTML(s.proto)}</textarea>`) +
      field('Request (JSON)', `<textarea data-field="request" rows="2">${escapeHTML(s.request)}</textarea>`)
    );
    case 'sleep': return (
      field('Duration', `<input data-field="duration" value="${escapeHTML(s.duration)}">`)
    );
  }
  return '';
}

function field(label, input) {
  return `<div class="field"><label>${label}</label>${input}</div>`;
}
function twoCol(a, b) {
  return `<div style="display:grid;grid-template-columns:1fr 1fr;gap:10px">${a}${b}</div>`;
}
function escapeHTML(s) {
  return String(s ?? '').replace(/[&<>"']/g, c => ({ '&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;' }[c]));
}

function wireFields(el, obj, idx, kind) {
  el.querySelectorAll('[data-field]').forEach(input => {
    input.addEventListener('input', () => {
      obj[input.dataset.field] = input.value;
      // Don't full-rerender (would lose focus) — just update YAML.
      yamlOut.textContent = emitYAML(state);
    });
    input.addEventListener('blur', () => {
      render();
    });
  });
}

// ----- Drag-and-drop reorder -----------------------------------------------

function wireDrag(el, kind) {
  el.addEventListener('dragstart', e => {
    el.classList.add('dragging');
    e.dataTransfer.setData('text/plain', el.dataset.id);
    e.dataTransfer.effectAllowed = 'move';
  });
  el.addEventListener('dragend', () => {
    el.classList.remove('dragging');
    document.querySelectorAll('.card.drop-target').forEach(c => c.classList.remove('drop-target'));
  });
  el.addEventListener('dragover', e => {
    e.preventDefault();
    document.querySelectorAll('.card.drop-target').forEach(c => c.classList.remove('drop-target'));
    el.classList.add('drop-target');
  });
  el.addEventListener('drop', e => {
    e.preventDefault();
    const fromId = e.dataTransfer.getData('text/plain');
    if (fromId === el.dataset.id) return;
    const from = state.steps.findIndex(s => s.id === fromId);
    const to   = state.steps.findIndex(s => s.id === el.dataset.id);
    if (from < 0 || to < 0) return;
    const moved = state.steps.splice(from, 1)[0];
    state.steps.splice(to, 0, moved);
    render();
  });
}

// ----- Sidebar wiring ------------------------------------------------------

function syncInputs() {
  document.getElementById('meta-name').value      = state.name;
  document.getElementById('meta-labels').value    = Object.entries(state.labels).map(([k,v])=>`${k}=${v}`).join('\n');
  document.getElementById('conn-kafka').value     = state.connectors.kafka.bootstrap_servers;
  document.getElementById('conn-http').value      = state.connectors.http.base_url;
  document.getElementById('conn-ws').value        = state.connectors.websocket.base_url;
  document.getElementById('conn-grpc').value      = state.connectors.grpc.address;
}

document.getElementById('meta-name').addEventListener('input', e => { state.name = e.target.value; yamlOut.textContent = emitYAML(state); });
document.getElementById('meta-labels').addEventListener('input', e => {
  state.labels = {};
  for (const line of e.target.value.split('\n')) {
    const [k, v] = line.split('=');
    if (k && v) state.labels[k.trim()] = v.trim();
  }
  yamlOut.textContent = emitYAML(state);
});
document.getElementById('conn-kafka').addEventListener('input', e => { state.connectors.kafka.bootstrap_servers = e.target.value; yamlOut.textContent = emitYAML(state); });
document.getElementById('conn-http').addEventListener('input',  e => { state.connectors.http.base_url = e.target.value; yamlOut.textContent = emitYAML(state); });
document.getElementById('conn-ws').addEventListener('input',    e => { state.connectors.websocket.base_url = e.target.value; yamlOut.textContent = emitYAML(state); });
document.getElementById('conn-grpc').addEventListener('input',  e => { state.connectors.grpc.address = e.target.value; yamlOut.textContent = emitYAML(state); });

document.querySelectorAll('[data-add]').forEach(btn => {
  btn.addEventListener('click', () => {
    state.steps.push(TEMPLATES[btn.dataset.add]());
    render();
  });
});
document.getElementById('btn-add-check').addEventListener('click', () => {
  state.checks.push({ id: nextId(), name: `check-${state.checks.length + 1}`, expr: "size(stream('orders')) >= 1", severity: 'critical' });
  render();
});

document.getElementById('btn-new').addEventListener('click', () => {
  if (!confirm('Discard the current scenario?')) return;
  Object.assign(state, JSON.parse(JSON.stringify(DEFAULT_STATE)));
  state.steps = [];
  state.checks = [];
  render();
});

document.getElementById('btn-copy').addEventListener('click', async () => {
  try {
    await navigator.clipboard.writeText(emitYAML(state));
    showToast('YAML copied to clipboard', 'ok');
  } catch (e) {
    showToast('Copy failed: ' + e.message, 'err');
  }
});

document.getElementById('btn-validate').addEventListener('click', async () => {
  const yaml = emitYAML(state);
  // No dedicated /validate endpoint yet — reuse /api/v1/scenarios with a
  // server-side dry-run query. Falls back to a local hint if the API isn't
  // reachable.
  try {
    const r = await fetch('/api/v1/scenarios?dry_run=1', {
      method: 'POST',
      headers: { 'Content-Type': 'application/yaml' },
      body: yaml,
    });
    if (r.ok) {
      showToast('Scenario is valid', 'ok');
    } else {
      const body = await r.text();
      showToast('Validation failed: ' + body, 'err');
    }
  } catch (e) {
    showToast('Cannot reach control plane: ' + e.message, 'err');
  }
});

document.getElementById('btn-save').addEventListener('click', async () => {
  const yaml = emitYAML(state);
  try {
    const r = await fetch('/api/v1/scenarios', {
      method: 'POST',
      headers: { 'Content-Type': 'application/yaml' },
      body: yaml,
    });
    const body = await r.json().catch(() => ({}));
    if (r.ok) {
      showToast(`Saved: ${body.name} v${body.version}`, 'ok');
    } else {
      showToast('Save failed: ' + (body.error || r.status), 'err');
    }
  } catch (e) {
    showToast('Cannot reach control plane: ' + e.message, 'err');
  }
});

function showToast(msg, cls) {
  toast.textContent = msg;
  toast.className = 'toast show ' + (cls || '');
  setTimeout(() => toast.className = 'toast', 4500);
}

// ----- Boot ----------------------------------------------------------------

// Pre-populate with the user-demo scenario for instant gratification.
state.name = 'user-demo';
state.connectors.kafka.bootstrap_servers = 'localhost:19092';
state.connectors.http.base_url = 'http://localhost:18082';
state.steps = [
  TEMPLATES.produce(),
  TEMPLATES.consume(),
  TEMPLATES.http(),
];
state.steps[0].topic = 'user-demo-orders';
state.steps[0].payload = '${data.orders}';
state.steps[0].count = 25;
state.steps[1].topic = 'user-demo-orders';
state.steps[1].group = 'edt-builder';
state.steps[1].match = 'payload.amount >= 0';
state.steps[2].method = 'GET';
state.steps[2].path = '/status/200';
state.checks = [
  { id: nextId(), name: 'produced_orders', expr: "size(stream('user-demo-orders')) >= 25", severity: 'critical' },
  { id: nextId(), name: 'httpbin_healthy', expr: "size(stream('http:/status/200')) >= 1", severity: 'critical' },
];

render();
