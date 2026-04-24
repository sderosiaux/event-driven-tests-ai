// Scenario builder — free-form canvas + live execution.
//
// Each step carries an (x, y) position and is rendered absolute-positioned.
// Drag by the card header to move freely. Execution order is derived from
// Y coordinates (top → bottom), so moving a card up/down reorders it. The
// emitted YAML reflects that order, live, in the right pane.
//
// "Run live" button posts the current YAML to /api/v1/run-adhoc. The CP
// executes it in-process against the real connectors declared in the
// scenario, persists the run, and returns the full report. The canvas
// highlights cards based on the verdict.

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
  // Data generators keyed by alias, carried through import/export so
  // scenarios that reference ${data.X} in their payloads keep working.
  data: {},
  steps: [],
  checks: [],
  lastReport: null,
};

const state = JSON.parse(JSON.stringify(DEFAULT_STATE));
let stepSeq = 0;
const nextId = () => `s${++stepSeq}`;

const GRID = 20;
const CARD_W = 320;
// Matches the vertical breathing room most card bodies actually need; used
// only as a fallback before the first render measures the real DOM height.
const CARD_H_FALLBACK = 220;
const LANE_STEPS   = 40;   // x coordinate for the step column
const LANE_CHECKS  = 440;  // x coordinate for the check column
const ROW_STEP     = 240;  // vertical spacing between consecutive cards

// ----- Step templates ------------------------------------------------------

const TEMPLATES = {
  produce: (pos) => ({ id: nextId(), type: 'produce', name: 'place-orders', topic: 'orders', payload: '${data.orders}', count: 10, rate: '', x: pos.x, y: pos.y }),
  consume: (pos) => ({ id: nextId(), type: 'consume', name: 'wait-ack', topic: 'orders.ack', group: 'edt-run', timeout: '5s', match: '', x: pos.x, y: pos.y }),
  http:    (pos) => ({ id: nextId(), type: 'http', name: 'call-api', method: 'GET', path: '/health', body: '', expectStatus: 200, x: pos.x, y: pos.y }),
  websocket:(pos)=> ({ id: nextId(), type: 'websocket', name: 'watch-stream', path: '/orders/stream', send: '', count: 0, timeout: '30s', match: '', x: pos.x, y: pos.y }),
  sse:      (pos) => ({ id: nextId(), type: 'sse', name: 'watch-events', path: '/events', count: 0, timeout: '30s', match: '', x: pos.x, y: pos.y }),
  grpc:     (pos) => ({ id: nextId(), type: 'grpc', name: 'rpc-call', proto: 'syntax = "proto3";\nmessage Empty {}\nservice S { rpc M(Empty) returns (Empty); }', method: 'pkg.S/M', request: '{}', expectCode: 0, x: pos.x, y: pos.y }),
  sleep:    (pos) => ({ id: nextId(), type: 'sleep', name: 'pause', duration: '1s', x: pos.x, y: pos.y }),
};

// ----- YAML emitter --------------------------------------------------------

const YAML_RESERVED = /^(?:true|false|null|yes|no|on|off|~)$/i;
function looksNumeric(s) {
  if (s === '' || /\s/.test(s)) return false;
  return Number.isFinite(Number(s));
}
function yamlEscape(s) {
  s = String(s);
  if (s === '') return '""';
  if (YAML_RESERVED.test(s)) return JSON.stringify(s);
  if (looksNumeric(s)) return JSON.stringify(s);
  if (s !== s.trim()) return JSON.stringify(s);
  if (/[:#@`\[\]{}|>*&!%,]|^-|^\?|^:/.test(s) || s.includes('\n')) {
    if (s.includes('\n')) return '|-\n' + s.split('\n').map(l => '  ' + l).join('\n');
    return JSON.stringify(s);
  }
  return s;
}

function emitValue(v, indent) {
  const pad = '  '.repeat(indent);
  if (v === null || v === undefined) return 'null';
  if (typeof v === 'boolean' || typeof v === 'number') return String(v);
  if (typeof v === 'string') {
    if (v.includes('\n')) return '|-\n' + v.split('\n').map(l => pad + '  ' + l).join('\n');
    return yamlEscape(v);
  }
  if (Array.isArray(v)) {
    if (v.length === 0) return '[]';
    return '\n' + v.map(item => {
      if (item === null || item === undefined || typeof item !== 'object') {
        return pad + '- ' + emitValue(item, indent + 1);
      }
      if (Array.isArray(item)) return pad + '-' + emitValue(item, indent + 2);
      const keys = Object.keys(item);
      if (keys.length === 0) return pad + '- {}';
      const childPad = '  '.repeat(indent + 1);
      return keys.map((k, i) => {
        const rendered = emitValue(item[k], indent + 2);
        const prefix = i === 0 ? pad + '- ' : childPad;
        if (rendered.startsWith('\n')) return prefix + k + ':' + rendered;
        return prefix + k + ': ' + rendered;
      }).join('\n');
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
  const body = emitValue(toWireShape(s), 0);
  return body.startsWith('\n') ? body.slice(1) : body;
}

function orderedSteps(s) {
  return [...s.steps].sort((a, b) => (a.y - b.y) || (a.x - b.x));
}

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
  // Carry data generators through — if the imported scenario had them we
  // preserve the exact shape so ${data.alias} references still resolve.
  if (s.data && Object.keys(s.data).length) out.spec.data = s.data;
  out.spec.steps = orderedSteps(s).map(stepToWire);
  if (s.checks.length) out.spec.checks = s.checks.map(c => ({ name: c.name, expr: c.expr, ...(c.severity ? { severity: c.severity } : {}) }));
  return out;
}

function stepToWire(st) {
  switch (st.type) {
    case 'produce':  return { name: st.name, produce: dropEmpty({ topic: st.topic, payload: st.payload, count: num(st.count), rate: st.rate }) };
    case 'consume':  return { name: st.name, consume: dropEmpty({ topic: st.topic, group: st.group, timeout: st.timeout, match: matchArr(st.match) }) };
    case 'http':     return { name: st.name, http: dropEmpty({ method: st.method, path: st.path, body: st.body, expect: st.expectStatus ? { status: num(st.expectStatus) } : undefined }) };
    case 'websocket':return { name: st.name, websocket: dropEmpty({ path: st.path, send: st.send, count: num(st.count), timeout: st.timeout, match: matchArr(st.match) }) };
    case 'sse':      return { name: st.name, sse: dropEmpty({ path: st.path, count: num(st.count), timeout: st.timeout, match: matchArr(st.match) }) };
    case 'grpc':     return { name: st.name, grpc: dropEmpty({ proto: st.proto, method: st.method, request: st.request, expect: st.expectCode !== undefined && st.expectCode !== '' ? { code: num(st.expectCode) } : undefined }) };
    case 'sleep':    return { name: st.name, sleep: st.duration };
  }
}
function matchArr(raw) {
  if (!raw || !raw.trim()) return undefined;
  return raw.split('\n').map(l => l.trim()).filter(Boolean).map(key => ({ key }));
}
function num(v) {
  if (v === '' || v === null || v === undefined) return undefined;
  const n = Number(v);
  return Number.isFinite(n) && n !== 0 ? n : undefined;
}
function dropEmpty(o) {
  for (const k of Object.keys(o)) if (o[k] === '' || o[k] === undefined || o[k] === null) delete o[k];
  return o;
}

// ----- DOM -----------------------------------------------------------------

const canvas = document.getElementById('canvas');
const yamlOut = document.getElementById('yaml-out');
const toast = document.getElementById('toast');
const resultsPane = document.getElementById('results');

function render() {
  renderCanvas();
  yamlOut.textContent = emitYAML(state);
  syncInputs();
  renderResults();
}

// The canvas is sized generously (INFINITE_W × INFINITE_H) so the user can
// drag cards well outside the initial viewport. It always grows to fit
// whatever is the furthest card plus a margin, so "dragging into new space"
// never runs out of room.
const INFINITE_W = 6000;
const INFINITE_H = 6000;

function computeCanvasSize() {
  const all = [...state.steps, ...state.checks];
  const maxX = all.reduce((m, c) => Math.max(m, (c.x || 0) + CARD_W), 0) + 400;
  const maxY = all.reduce((m, c) => Math.max(m, (c.y || 0) + (cardHeight(c.id) || CARD_H_FALLBACK)), 0) + 400;
  return { w: Math.max(INFINITE_W, maxX), h: Math.max(INFINITE_H, maxY) };
}

function cardHeight(id) {
  const el = document.querySelector(`.card[data-id="${id}"]`);
  return el ? el.offsetHeight : null;
}

function renderCanvas() {
  const { w, h } = computeCanvasSize();
  canvas.style.width = w + 'px';
  canvas.style.height = h + 'px';
  canvas.innerHTML = '';

  if (state.steps.length === 0 && state.checks.length === 0) {
    const empty = document.createElement('div');
    empty.className = 'canvas-empty';
    empty.innerHTML = `<h3>Empty canvas</h3>
      <p>Add a step from the palette and drag it anywhere. Drag the empty background to pan. Execution order = Y position (top to bottom).</p>`;
    canvas.appendChild(empty);
    return;
  }

  canvas.appendChild(connectionsSVG(w, h));
  state.steps.forEach(step => canvas.appendChild(cardForStep(step)));
  state.checks.forEach(check => canvas.appendChild(cardForCheck(check)));
  // After cards mount we know their real heights — refine arrow start points.
  requestAnimationFrame(redrawConnectionsOnly);
}

function connectionsSVG(w, h) {
  const svgNS = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(svgNS, 'svg');
  svg.setAttribute('class', 'connections');
  svg.setAttribute('width', w);
  svg.setAttribute('height', h);

  // Only link STEPS (not checks) in Y-order. Skip pairs where the next step
  // isn't strictly below — otherwise the arrow loops back and crosses other
  // cards (user feedback: "weird arrows").
  const ordered = orderedSteps(state);
  for (let i = 0; i < ordered.length - 1; i++) {
    const a = ordered[i], b = ordered[i + 1];
    if (b.y <= a.y) continue; // skip same-row or backward pairs

    const ah = cardHeight(a.id) || CARD_H_FALLBACK;
    const ax = a.x + CARD_W / 2;
    const ay = a.y + ah;          // bottom of source card
    const bx = b.x + CARD_W / 2;
    const by = b.y;                // top of target card
    // Orthogonal L-routing: down to midpoint, horizontal, down. Keeps lines
    // off of other cards in the lanes.
    const midY = (ay + by) / 2;
    const path = document.createElementNS(svgNS, 'path');
    const d = ax === bx
      ? `M ${ax} ${ay} L ${bx} ${by - 6}`
      : `M ${ax} ${ay} L ${ax} ${midY} L ${bx} ${midY} L ${bx} ${by - 6}`;
    path.setAttribute('d', d);
    path.setAttribute('class', 'connection');
    svg.appendChild(path);
    const arrow = document.createElementNS(svgNS, 'polygon');
    arrow.setAttribute('points', `${bx-5},${by-7} ${bx+5},${by-7} ${bx},${by}`);
    arrow.setAttribute('class', 'connection-arrow');
    svg.appendChild(arrow);
  }

  // Draw a subtle dashed link from each check to the step whose output it
  // references, so users see at a glance which step each assertion covers.
  for (const check of state.checks) {
    const ref = findReferencedStep(check.expr);
    if (!ref) continue;
    const rh = cardHeight(ref.id) || CARD_H_FALLBACK;
    const from = { x: ref.x + CARD_W, y: ref.y + rh / 2 };
    const to   = { x: check.x,         y: (check.y || 0) + 40 };
    const path = document.createElementNS(svgNS, 'path');
    const midX = (from.x + to.x) / 2;
    path.setAttribute('d', `M ${from.x} ${from.y} C ${midX} ${from.y}, ${midX} ${to.y}, ${to.x} ${to.y}`);
    path.setAttribute('class', 'connection check-link');
    svg.appendChild(path);
  }
  return svg;
}

// Match stream('topic') / stream("topic") inside a CEL expression to a step.
function findReferencedStep(expr) {
  if (!expr) return null;
  const m = /stream\(\s*['"]([^'"]+)['"]\s*\)/.exec(expr);
  if (!m) return null;
  const target = m[1];
  return state.steps.find(s => expectedStream(s) === target);
}

function cardForStep(step) {
  const el = document.createElement('article');
  el.className = `card type-${step.type}`;
  el.dataset.id = step.id;
  el.style.left = step.x + 'px';
  el.style.top  = step.y + 'px';

  const v = verdictForStep(step);
  if (v) el.classList.add('verdict-' + v);

  el.innerHTML = `
    <header>
      <span class="type-badge">${step.type}</span>
      <input class="title" data-field="name" value="${escapeHTML(step.name)}" placeholder="step name">
      <button class="remove" title="Remove step">×</button>
    </header>
    <div class="body">${bodyForStep(step)}</div>`;
  el.querySelector('.remove').addEventListener('click', e => {
    e.stopPropagation();
    state.steps = state.steps.filter(s => s.id !== step.id);
    render();
  });
  wireFields(el, step);
  wireDragCard(el, step);
  return el;
}

function cardForCheck(check) {
  const el = document.createElement('article');
  el.className = `card type-check`;
  el.dataset.id = check.id;
  el.style.left = (check.x || 40) + 'px';
  el.style.top  = (check.y || 40) + 'px';

  const v = verdictForCheck(check);
  if (v) el.classList.add('verdict-' + v);

  el.innerHTML = `
    <header>
      <span class="type-badge">check</span>
      <input class="title" data-field="name" value="${escapeHTML(check.name)}" placeholder="check name">
      <button class="remove" title="Remove check">×</button>
    </header>
    <div class="body">
      <div class="field"><label>CEL expression</label>
        <textarea data-field="expr" rows="2">${escapeHTML(check.expr)}</textarea>
      </div>
      <div class="field"><label>Severity</label>
        <select data-field="severity">
          <option value="critical" ${check.severity==='critical'?'selected':''}>critical</option>
          <option value="warning"  ${check.severity==='warning'?'selected':''}>warning</option>
          <option value="info"     ${check.severity==='info'?'selected':''}>info</option>
        </select>
      </div>
    </div>`;
  el.querySelector('.remove').addEventListener('click', e => {
    e.stopPropagation();
    state.checks = state.checks.filter(c => c.id !== check.id);
    render();
  });
  wireFields(el, check);
  wireDragCard(el, check);
  return el;
}

function bodyForStep(s) {
  const f = (label, input) => `<div class="field"><label>${label}</label>${input}</div>`;
  const two = (a, b) => `<div style="display:grid;grid-template-columns:1fr 1fr;gap:8px">${a}${b}</div>`;
  switch (s.type) {
    case 'produce': return (
      two(f('Topic', `<input data-field="topic" value="${escapeHTML(s.topic)}">`),
          f('Count', `<input data-field="count" value="${s.count??''}" type="number">`)) +
      f('Payload', `<textarea data-field="payload" rows="2">${escapeHTML(s.payload)}</textarea>`) +
      f('Rate (e.g. 50/s, optional)', `<input data-field="rate" value="${escapeHTML(s.rate||'')}">`)
    );
    case 'consume': return (
      two(f('Topic', `<input data-field="topic" value="${escapeHTML(s.topic)}">`),
          f('Group', `<input data-field="group" value="${escapeHTML(s.group)}">`)) +
      two(f('Timeout', `<input data-field="timeout" value="${escapeHTML(s.timeout)}">`), '') +
      f('Match (CEL, one per line)', `<textarea data-field="match" rows="2">${escapeHTML(s.match||'')}</textarea>`)
    );
    case 'http': return (
      two(f('Method', `<select data-field="method">${['GET','POST','PUT','PATCH','DELETE'].map(m => `<option ${m===s.method?'selected':''}>${m}</option>`).join('')}</select>`),
          f('Path', `<input data-field="path" value="${escapeHTML(s.path)}">`)) +
      f('Body (JSON, optional)', `<textarea data-field="body" rows="2">${escapeHTML(s.body||'')}</textarea>`) +
      f('Expect status', `<input data-field="expectStatus" value="${s.expectStatus??''}" type="number">`)
    );
    case 'websocket': return (
      two(f('Path', `<input data-field="path" value="${escapeHTML(s.path)}">`),
          f('Timeout', `<input data-field="timeout" value="${escapeHTML(s.timeout)}">`)) +
      f('Send (optional JSON)', `<textarea data-field="send" rows="2">${escapeHTML(s.send||'')}</textarea>`) +
      two(f('Count', `<input data-field="count" value="${s.count??''}" type="number">`), '') +
      f('Match', `<textarea data-field="match" rows="2">${escapeHTML(s.match||'')}</textarea>`)
    );
    case 'sse': return (
      two(f('Path', `<input data-field="path" value="${escapeHTML(s.path)}">`),
          f('Timeout', `<input data-field="timeout" value="${escapeHTML(s.timeout)}">`)) +
      two(f('Count', `<input data-field="count" value="${s.count??''}" type="number">`), '') +
      f('Match', `<textarea data-field="match" rows="2">${escapeHTML(s.match||'')}</textarea>`)
    );
    case 'grpc': return (
      two(f('Method', `<input data-field="method" value="${escapeHTML(s.method)}">`),
          f('Expect code', `<input data-field="expectCode" value="${s.expectCode??''}" type="number">`)) +
      f('Inline .proto', `<textarea data-field="proto" rows="3">${escapeHTML(s.proto)}</textarea>`) +
      f('Request JSON', `<textarea data-field="request" rows="2">${escapeHTML(s.request)}</textarea>`)
    );
    case 'sleep': return f('Duration', `<input data-field="duration" value="${escapeHTML(s.duration)}">`);
  }
  return '';
}

function wireFields(el, obj) {
  el.querySelectorAll('[data-field]').forEach(input => {
    input.addEventListener('input', () => {
      obj[input.dataset.field] = input.value;
      yamlOut.textContent = emitYAML(state);
    });
    input.addEventListener('blur', () => render());
    input.addEventListener('mousedown', e => e.stopPropagation());
  });
}

// ----- Canvas pan ----------------------------------------------------------
// Dragging anywhere on the empty canvas (not on a card) pans the view, so
// the canvas feels infinite without hunting for scrollbars. Cursor flips to
// grabbing on press.

(function wireCanvasPan() {
  let panning = false, startX, startY, scrollL, scrollT;
  canvas.addEventListener('mousedown', e => {
    // Only pan when the click hit the canvas itself (not a card).
    if (e.target !== canvas) return;
    panning = true;
    startX = e.clientX;
    startY = e.clientY;
    scrollL = canvas.scrollLeft;
    scrollT = canvas.scrollTop;
    canvas.classList.add('panning');
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp, { once: true });
  });
  function onMove(e) {
    if (!panning) return;
    canvas.scrollLeft = scrollL - (e.clientX - startX);
    canvas.scrollTop  = scrollT - (e.clientY - startY);
  }
  function onUp() {
    panning = false;
    canvas.classList.remove('panning');
    document.removeEventListener('mousemove', onMove);
  }
})();

// ----- Card dragging ------------------------------------------------------

function wireDragCard(el, obj) {
  const header = el.querySelector('header');
  let startX, startY, origX, origY, dragging = false;

  header.addEventListener('mousedown', e => {
    if (e.target.tagName === 'INPUT' || e.target.tagName === 'BUTTON') return;
    e.preventDefault();
    dragging = true;
    startX = e.clientX; startY = e.clientY;
    origX = obj.x || 0; origY = obj.y || 0;
    el.classList.add('dragging');
    document.body.style.cursor = 'grabbing';
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp, { once: true });
  });

  function onMove(e) {
    if (!dragging) return;
    let nx = Math.max(0, origX + (e.clientX - startX));
    let ny = Math.max(0, origY + (e.clientY - startY));
    nx = Math.round(nx / GRID) * GRID;
    ny = Math.round(ny / GRID) * GRID;
    obj.x = nx; obj.y = ny;
    el.style.left = nx + 'px';
    el.style.top  = ny + 'px';
    yamlOut.textContent = emitYAML(state);
    redrawConnectionsOnly();
  }
  function onUp() {
    dragging = false;
    el.classList.remove('dragging');
    document.body.style.cursor = '';
    document.removeEventListener('mousemove', onMove);
    render();
  }
}

function redrawConnectionsOnly() {
  const existing = canvas.querySelector('svg.connections');
  const w = parseFloat(canvas.style.width) || canvas.offsetWidth;
  const h = parseFloat(canvas.style.height) || canvas.offsetHeight;
  const fresh = connectionsSVG(w, h);
  if (existing) canvas.replaceChild(fresh, existing);
  else canvas.insertBefore(fresh, canvas.firstChild);
}

// ----- Verdict overlay -----------------------------------------------------

function verdictForStep(step) {
  const rep = state.lastReport;
  if (!rep || rep.running) return null;
  if (rep.status === 'error') return 'error';
  const expected = expectedStream(step);
  if (!expected) return null;
  const match = (rep.checks || []).find(c =>
    (c.expr || '').includes(`'${expected}'`) ||
    (c.expr || '').includes(`"${expected}"`)
  );
  if (!match) return null;
  return match.passed ? 'pass' : 'fail';
}
function verdictForCheck(check) {
  const rep = state.lastReport;
  if (!rep || rep.running) return null;
  const c = (rep.checks || []).find(c => c.name === check.name);
  if (!c) return null;
  return c.passed ? 'pass' : 'fail';
}
function expectedStream(step) {
  switch (step.type) {
    case 'produce':
    case 'consume':  return step.topic || null;
    case 'http':     return step.path ? 'http:' + step.path : null;
    case 'websocket':return step.path ? 'ws:' + step.path : null;
    case 'sse':      return step.path ? 'sse:' + step.path : null;
    case 'grpc':     return step.method ? 'grpc:' + step.method : null;
  }
  return null;
}

// ----- Sidebar wiring -----------------------------------------------------

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
  for (const line of e.target.value.split('\n')) { const [k, v] = line.split('='); if (k && v) state.labels[k.trim()] = v.trim(); }
  yamlOut.textContent = emitYAML(state);
});
document.getElementById('conn-kafka').addEventListener('input', e => { state.connectors.kafka.bootstrap_servers = e.target.value; yamlOut.textContent = emitYAML(state); });
document.getElementById('conn-http').addEventListener('input',  e => { state.connectors.http.base_url = e.target.value; yamlOut.textContent = emitYAML(state); });
document.getElementById('conn-ws').addEventListener('input',    e => { state.connectors.websocket.base_url = e.target.value; yamlOut.textContent = emitYAML(state); });
document.getElementById('conn-grpc').addEventListener('input',  e => { state.connectors.grpc.address = e.target.value; yamlOut.textContent = emitYAML(state); });

document.querySelectorAll('[data-add]').forEach(btn => {
  btn.addEventListener('click', () => {
    const pos = nextFreeSlot('step');
    state.steps.push(TEMPLATES[btn.dataset.add](pos));
    render();
    // Scroll the canvas so the new card is visible — users dropping into an
    // existing flow shouldn't have to go hunt for what they just added.
    scrollCardIntoView(state.steps[state.steps.length - 1]);
  });
});
document.getElementById('btn-add-check').addEventListener('click', () => {
  const pos = nextFreeSlot('check');
  state.checks.push({ id: nextId(), name: `check-${state.checks.length + 1}`, expr: "size(stream('orders')) >= 1", severity: 'critical', x: pos.x, y: pos.y });
  render();
  scrollCardIntoView(state.checks[state.checks.length - 1]);
});

function scrollCardIntoView(c) {
  const wrap = document.querySelector('.canvas-wrap .canvas');
  if (!wrap || !c) return;
  wrap.scrollTo({
    left: Math.max(0, c.x - 60),
    top:  Math.max(0, c.y - 60),
    behavior: 'smooth',
  });
}

// New card placement: append at the bottom of the step lane (or check lane
// for a check). Keeps the default layout tidy while still letting the user
// drag anywhere afterwards.
function nextFreeSlot(kind) {
  const lane = kind === 'check' ? LANE_CHECKS : LANE_STEPS;
  const siblings = kind === 'check' ? state.checks : state.steps;
  const maxY = siblings.reduce((m, c) => Math.max(m, (c.y || 0)), -ROW_STEP);
  return { x: lane, y: maxY + ROW_STEP };
}

document.getElementById('btn-new').addEventListener('click', () => {
  if (state.steps.length + state.checks.length > 0 && !confirm('Discard the current scenario?')) return;
  Object.assign(state, JSON.parse(JSON.stringify(DEFAULT_STATE)));
  state.steps = []; state.checks = []; state.lastReport = null;
  render();
});

document.getElementById('btn-copy').addEventListener('click', async () => {
  try { await navigator.clipboard.writeText(emitYAML(state)); showToast('YAML copied', 'ok'); }
  catch (e) { showToast('Copy failed: ' + e.message, 'err'); }
});

document.getElementById('btn-validate').addEventListener('click', async () => {
  try {
    const r = await fetch('/api/v1/scenarios?dry_run=1', { method: 'POST', headers: { 'Content-Type': 'application/yaml' }, body: emitYAML(state) });
    if (r.ok) showToast('Scenario is valid', 'ok');
    else showToast('Validation failed: ' + await r.text(), 'err');
  } catch (e) { showToast('Cannot reach control plane: ' + e.message, 'err'); }
});

document.getElementById('btn-save').addEventListener('click', async () => {
  try {
    const r = await fetch('/api/v1/scenarios', { method: 'POST', headers: { 'Content-Type': 'application/yaml' }, body: emitYAML(state) });
    const body = await r.json().catch(() => ({}));
    if (r.ok) showToast(`Saved: ${body.name} v${body.version}`, 'ok');
    else showToast('Save failed: ' + (body.error || r.status), 'err');
  } catch (e) { showToast('Cannot reach control plane: ' + e.message, 'err'); }
});

document.getElementById('btn-run').addEventListener('click', async () => {
  const btn = document.getElementById('btn-run');
  btn.disabled = true; btn.textContent = 'Running…';
  state.lastReport = { running: true };
  renderResults();
  try {
    const r = await fetch('/api/v1/run-adhoc', { method: 'POST', headers: { 'Content-Type': 'application/yaml' }, body: emitYAML(state) });
    const body = await r.json().catch(() => ({}));
    if (r.ok || r.status === 202) {
      state.lastReport = body.report || null;
      if (body.error) showToast('Run finished with error: ' + body.error, 'err');
      else showToast(`Run ${state.lastReport?.status || 'done'}`, state.lastReport?.status === 'pass' ? 'ok' : 'err');
    } else {
      state.lastReport = null;
      showToast('Run failed: ' + (body.error || r.status), 'err');
    }
  } catch (e) {
    state.lastReport = null;
    showToast('Cannot reach control plane: ' + e.message, 'err');
  } finally {
    btn.disabled = false; btn.textContent = 'Run live';
    render();
  }
});

function renderResults() {
  const rep = state.lastReport;
  if (!rep) {
    resultsPane.innerHTML = `<p class="muted">No run yet. Click <strong>Run live</strong> to execute this scenario against the real stack.</p>`;
    return;
  }
  if (rep.running) {
    resultsPane.innerHTML = `<p class="muted">Running…</p>`;
    return;
  }
  const checkRows = (rep.checks || []).map(c =>
    `<tr>
       <td class="status-${c.passed ? 'pass' : 'fail'}">${c.passed ? 'PASS' : 'FAIL'}</td>
       <td>${escapeHTML(c.name)}</td>
       <td class="muted">${escapeHTML(c.severity || '—')}</td>
       <td class="muted"><code>${escapeHTML(String(c.value ?? '—'))}</code></td>
       <td class="muted">${escapeHTML(c.err || '—')}</td>
     </tr>`).join('');
  const status = (rep.status || 'unknown');
  resultsPane.innerHTML = `
    <div class="result-head status-${escapeHTML(status)}">
      <strong>${escapeHTML(status.toUpperCase())}</strong>
      <span class="muted">${escapeHTML(rep.run_id || rep.RunID || '')}</span>
      <span class="muted">${rep.event_count ?? rep.EventCount ?? 0} events</span>
      <span class="muted">${rep.duration ? (rep.duration / 1e9).toFixed(2) + 's' : ''}</span>
      ${rep.run_id ? `<a href="/ui/runs/${encodeURIComponent(rep.run_id)}" class="spaced">open detail →</a>` : ''}
    </div>
    ${rep.error ? `<p class="run-error">${escapeHTML(rep.error)}</p>` : ''}
    ${checkRows ? `<table><thead><tr><th></th><th>Check</th><th>Severity</th><th>Value</th><th>Error</th></tr></thead><tbody>${checkRows}</tbody></table>` : ''}
  `;
}

function showToast(msg, cls) {
  toast.textContent = msg;
  toast.className = 'toast show ' + (cls || '');
  setTimeout(() => toast.className = 'toast', 4500);
}

function escapeHTML(s) {
  return String(s ?? '').replace(/[&<>"']/g, c => ({ '&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;' }[c]));
}

// ----- Boot ----------------------------------------------------------------
//
// Two boot modes:
//  1. No ?scenario= → pre-populate with a runnable demo scenario so the
//     user's first view isn't an empty canvas.
//  2. ?scenario=X → fetch the stored scenario + its parsed struct from the
//     API and hydrate the builder state. Users coming from the Scenarios
//     list land directly in the editor with diagram + YAML + metadata
//     already rendered.

function seedDefaultDemo() {
  state.name = 'user-demo';
  state.connectors.kafka.bootstrap_servers = 'localhost:19092';
  state.connectors.http.base_url = 'http://localhost:18082';
  state.steps = [
    Object.assign(TEMPLATES.produce({ x: LANE_STEPS, y: 40 }),                 { topic: 'user-demo-orders', payload: '${data.orders}', count: 25, name: 'place-orders' }),
    Object.assign(TEMPLATES.consume({ x: LANE_STEPS, y: 40 + ROW_STEP }),      { topic: 'user-demo-orders', group: 'edt-builder', match: 'payload.amount >= 0', name: 'consume-back' }),
    Object.assign(TEMPLATES.http   ({ x: LANE_STEPS, y: 40 + ROW_STEP * 2 }),  { method: 'GET',  path: '/status/200', name: 'gateway-healthz' }),
    Object.assign(TEMPLATES.http   ({ x: LANE_STEPS, y: 40 + ROW_STEP * 3 }),  { name: 'post-to-httpbin', method: 'POST', path: '/anything', body: '{"scenario":"user-demo"}' }),
  ];
  state.checks = [
    { id: nextId(), name: 'produced_orders', expr: "size(stream('user-demo-orders')) >= 25", severity: 'critical', x: LANE_CHECKS, y: 40 },
    { id: nextId(), name: 'httpbin_healthy', expr: "size(stream('http:/status/200')) >= 1",  severity: 'critical', x: LANE_CHECKS, y: 40 + ROW_STEP * 2 },
  ];
}

// Hydrate state from a parsed scenario coming from the API. Steps line up
// vertically in the steps lane; checks align in a parallel lane so the
// visual flow matches the execution order.
function hydrateFromParsed(parsed, yamlBody) {
  state.name = parsed?.metadata?.name || 'imported';
  state.labels = { ...(parsed?.metadata?.labels || {}) };
  const c = parsed?.spec?.connectors || {};
  state.connectors = {
    kafka:     { bootstrap_servers: c.kafka?.bootstrap_servers || '' },
    http:      { base_url: c.http?.base_url || '' },
    websocket: { base_url: c.websocket?.base_url || '' },
    grpc:      { address:  c.grpc?.address || '' },
  };
  // Preserve data generators verbatim — steps reference them via ${data.X}
  // and we don't have a UI for editing generator config yet.
  state.data = { ...(parsed?.spec?.data || {}) };
  state.steps = (parsed?.spec?.steps || []).map((s, i) => stepFromParsed(s, i));
  state.checks = (parsed?.spec?.checks || []).map((c, i) => ({
    id: nextId(),
    name: c.name || `check-${i + 1}`,
    expr: c.expr || '',
    severity: c.severity || 'critical',
    x: LANE_CHECKS,
    y: 40 + i * ROW_STEP,
  }));
  state.lastReport = null;
  // Stash the original YAML for fidelity — the emitter will re-emit from
  // state, but users comparing "is this the same scenario?" will see the
  // round-trip. (Not currently rendered; noted for future diff UI.)
  state._sourceYAML = yamlBody;
}

function stepFromParsed(raw, i) {
  const pos = { x: LANE_STEPS, y: 40 + i * ROW_STEP };
  if (raw.produce)   return { ...TEMPLATES.produce(pos),   name: raw.name, topic: raw.produce.topic || '', payload: raw.produce.payload || '', count: raw.produce.count || 0, rate: raw.produce.rate || '' };
  if (raw.consume)   return { ...TEMPLATES.consume(pos),   name: raw.name, topic: raw.consume.topic || '', group: raw.consume.group || '', timeout: raw.consume.timeout || '', match: (raw.consume.match || []).map(m => m.key).join('\n') };
  if (raw.http)      return { ...TEMPLATES.http(pos),      name: raw.name, method: raw.http.method || 'GET', path: raw.http.path || '', body: raw.http.body || '', expectStatus: raw.http.expect?.status || '' };
  if (raw.websocket) return { ...TEMPLATES.websocket(pos), name: raw.name, path: raw.websocket.path || '', send: raw.websocket.send || '', count: raw.websocket.count || 0, timeout: raw.websocket.timeout || '', match: (raw.websocket.match || []).map(m => m.key).join('\n') };
  if (raw.sse)       return { ...TEMPLATES.sse(pos),       name: raw.name, path: raw.sse.path || '', count: raw.sse.count || 0, timeout: raw.sse.timeout || '', match: (raw.sse.match || []).map(m => m.key).join('\n') };
  if (raw.grpc)      return { ...TEMPLATES.grpc(pos),      name: raw.name, proto: raw.grpc.proto || '', method: raw.grpc.method || '', request: raw.grpc.request || '{}', expectCode: raw.grpc.expect?.code ?? 0 };
  if (raw.sleep)     return { ...TEMPLATES.sleep(pos),     name: raw.name, duration: raw.sleep };
  return { ...TEMPLATES.sleep(pos), name: raw.name || `step-${i + 1}`, duration: '0s' };
}

async function boot() {
  const params = new URLSearchParams(location.search);
  const scenarioName = params.get('scenario');
  if (!scenarioName) {
    seedDefaultDemo();
    render();
    return;
  }
  try {
    const scn = await fetchJSON('/api/v1/scenarios/' + encodeURIComponent(scenarioName));
    if (scn.parsed) {
      hydrateFromParsed(scn.parsed, scn.yaml);
    } else {
      // Fallback when the server couldn't parse (shouldn't happen given we
      // only persist valid YAML) — seed empty state and surface the raw YAML.
      state.name = scenarioName;
      state.steps = [];
      state.checks = [];
      showToast('Loaded ' + scenarioName + ' (YAML only, parse failed)', 'err');
    }
  } catch (e) {
    seedDefaultDemo();
    showToast('Could not load ' + scenarioName + ': ' + e.message, 'err');
  }
  render();
}

async function fetchJSON(url) {
  const r = await fetch(url, { headers: { Accept: 'application/json' } });
  if (!r.ok) throw new Error(`${url}: ${r.status}`);
  return r.json();
}

boot();
