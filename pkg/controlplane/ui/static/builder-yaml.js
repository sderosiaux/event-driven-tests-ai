// YAML preview rendering: syntax highlighter + crumb sync + read-only
// readout of agent_under_test / evals (which the canvas can't edit yet
// but must surface so users see what's in the scenario).
//
// Extracted from builder.js to stay under the file size cap. Reads
// shared state via window.state (set early in builder.js); the DOM refs
// are looked up at call time so HTML changes don't require coordination.

function highlightYAML(text) {
  const esc = (s) => s.replace(/[&<>]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;'}[c]));
  return esc(text)
    .replace(/(#.*)$/gm, '<span class="tok-comment">$1</span>')
    .replace(/^(\s*)(- )/gm, '$1<span class="tok-dash">$2</span>')
    .replace(/^(\s*)([A-Za-z_][\w.-]*)(:)/gm, '$1<span class="tok-key">$2</span>$3')
    .replace(/('[^']*'|&quot;[^&]*?&quot;)/g, '<span class="tok-string">$1</span>')
    .replace(/(: )(-?\d+(?:\.\d+)?)(\s|$)/g, '$1<span class="tok-num">$2</span>$3')
    .replace(/(: )(true|false|null|~)(\s|$)/g, '$1<span class="tok-bool">$2</span>$3');
}

function renderYAML(text) {
  const yamlOut = document.getElementById('yaml-out');
  const crumbName = document.getElementById('crumb-name');
  if (yamlOut) yamlOut.innerHTML = highlightYAML(text);
  if (crumbName) crumbName.textContent = (window.state && window.state.name) || 'new';
  renderEvalsReadout();
}

// Read-only summary of the agent_under_test + evals blocks. The builder
// can't edit them yet, but hiding them entirely was misleading: users
// opened llm-summary-quality and assumed it had no evals because nothing
// in the UI mentioned the rubric. Show a concise readout in the palette.
function renderEvalsReadout() {
  const section = document.getElementById('evals-section');
  const out = document.getElementById('evals-readout');
  if (!section || !out) return;
  const st = window.state || {};
  const aut = st.agent_under_test;
  const evals = st.evals || [];
  if (!aut && evals.length === 0) {
    section.hidden = true;
    out.innerHTML = '';
    return;
  }
  section.hidden = false;
  const esc = (s) => String(s ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  let html = '';
  if (aut) {
    html += `<div class="readout-aut">
      <div class="readout-label">Agent</div>
      <div class="readout-value">${esc(aut.name || '—')}</div>
      <div class="readout-row">
        <span class="readout-arrow">←</span>
        <span class="readout-list mono">${(aut.consumes || []).map(esc).join(', ') || '—'}</span>
      </div>
      <div class="readout-row">
        <span class="readout-arrow">→</span>
        <span class="readout-list mono">${(aut.produces || []).map(esc).join(', ') || '—'}</span>
      </div>
    </div>`;
  }
  if (evals.length) {
    html += `<div class="readout-evals">
      <div class="readout-label">Evals (${evals.length})</div>
      ${evals.map(ev => {
        const judge = ev.judge || {};
        const thr = ev.threshold || {};
        const sev = ev.severity || 'warning';
        return `<div class="readout-eval">
          <div class="readout-eval-head">
            <span class="readout-eval-name">${esc(ev.name)}</span>
            <span class="sev-badge sev-${esc(sev)}">${esc(sev)}</span>
          </div>
          <div class="readout-eval-meta mono">judge ${esc(judge.model || '—')}</div>
          <div class="readout-eval-thr mono">${esc(thr.aggregate || 'avg')} ${esc(thr.value || '—')}${thr.over ? ' over ' + esc(thr.over) : ''}</div>
        </div>`;
      }).join('')}
    </div>`;
  }
  out.innerHTML = html;
}

// Drag-to-resize handle between the canvas and the YAML pane. Persists
// the chosen width to localStorage so it survives reloads. Width is
// applied via the `--yaml-width` CSS variable on .builder-layout, which
// drives the grid-template-columns value.
const YAML_WIDTH_KEY = 'edt.builder.yaml-width';
const YAML_WIDTH_MIN = 240;
const YAML_WIDTH_MAX_FRAC = 0.65; // never let the YAML pane eat more than 65% of the viewport
function applyYamlWidth(px) {
  const layout = document.querySelector('.builder-layout');
  if (!layout) return;
  const max = Math.floor(window.innerWidth * YAML_WIDTH_MAX_FRAC);
  const clamped = Math.max(YAML_WIDTH_MIN, Math.min(max, Math.round(px)));
  layout.style.setProperty('--yaml-width', clamped + 'px');
  return clamped;
}
function wireYamlResize() {
  const handle = document.getElementById('yaml-resize');
  const layout = document.querySelector('.builder-layout');
  if (!handle || !layout) return;
  // Restore previously chosen width.
  const saved = parseInt(localStorage.getItem(YAML_WIDTH_KEY) || '', 10);
  if (saved > 0) applyYamlWidth(saved);

  let dragging = false;
  const onDown = (e) => {
    dragging = true;
    layout.classList.add('resizing');
    e.preventDefault();
  };
  const onMove = (e) => {
    if (!dragging) return;
    // YAML width = distance from mouse to right edge of viewport. Tracks
    // the cursor exactly while dragging the handle.
    const w = applyYamlWidth(window.innerWidth - e.clientX);
    if (w != null) localStorage.setItem(YAML_WIDTH_KEY, String(w));
  };
  const onUp = () => {
    if (!dragging) return;
    dragging = false;
    layout.classList.remove('resizing');
  };
  handle.addEventListener('mousedown', onDown);
  document.addEventListener('mousemove', onMove);
  document.addEventListener('mouseup', onUp);
  // Double-click → reset to default 380px.
  handle.addEventListener('dblclick', () => {
    applyYamlWidth(380);
    localStorage.removeItem(YAML_WIDTH_KEY);
  });
}
wireYamlResize();
