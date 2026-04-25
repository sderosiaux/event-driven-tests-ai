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

// Resize + collapse for the YAML pane. Three persisted bits of state:
//   edt.builder.yaml-width     — last expanded width in px (240..max)
//   edt.builder.yaml-collapsed — '1' when the pane is in rail mode
// Width drives the --yaml-width CSS variable; collapsed adds a class
// to .builder-layout that swaps the pane for a 28px vertical rail.
const YAML_WIDTH_KEY     = 'edt.builder.yaml-width';
const YAML_COLLAPSED_KEY = 'edt.builder.yaml-collapsed';
const YAML_WIDTH_MIN = 240;
const YAML_WIDTH_DEFAULT = 380;
const YAML_RAIL_WIDTH = 28;
const YAML_WIDTH_MAX_FRAC = 0.65;

function applyYamlWidth(px) {
  const layout = document.querySelector('.builder-layout');
  if (!layout) return;
  const max = Math.floor(window.innerWidth * YAML_WIDTH_MAX_FRAC);
  const clamped = Math.max(YAML_WIDTH_MIN, Math.min(max, Math.round(px)));
  layout.style.setProperty('--yaml-width', clamped + 'px');
  return clamped;
}
function setYamlCollapsed(collapsed) {
  const layout = document.querySelector('.builder-layout');
  if (!layout) return;
  if (collapsed) {
    layout.classList.add('yaml-collapsed');
    layout.style.setProperty('--yaml-width', YAML_RAIL_WIDTH + 'px');
    localStorage.setItem(YAML_COLLAPSED_KEY, '1');
  } else {
    layout.classList.remove('yaml-collapsed');
    const saved = parseInt(localStorage.getItem(YAML_WIDTH_KEY) || '', 10);
    applyYamlWidth(saved > 0 ? saved : YAML_WIDTH_DEFAULT);
    localStorage.removeItem(YAML_COLLAPSED_KEY);
  }
}
function wireYamlResize() {
  const handle = document.getElementById('yaml-resize');
  const layout = document.querySelector('.builder-layout');
  if (!handle || !layout) return;
  // Restore prior width + collapsed state.
  const savedWidth = parseInt(localStorage.getItem(YAML_WIDTH_KEY) || '', 10);
  if (savedWidth > 0) applyYamlWidth(savedWidth);
  if (localStorage.getItem(YAML_COLLAPSED_KEY) === '1') setYamlCollapsed(true);

  let dragging = false;
  const onDown = (e) => {
    if (layout.classList.contains('yaml-collapsed')) return; // no drag in rail mode
    dragging = true;
    layout.classList.add('resizing');
    e.preventDefault();
  };
  const onMove = (e) => {
    if (!dragging) return;
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
  handle.addEventListener('dblclick', () => {
    applyYamlWidth(YAML_WIDTH_DEFAULT);
    localStorage.removeItem(YAML_WIDTH_KEY);
  });

  // Collapse / expand affordances.
  const collapseBtn = document.getElementById('btn-yaml-collapse');
  if (collapseBtn) collapseBtn.addEventListener('click', () => setYamlCollapsed(true));
  const rail = document.getElementById('yaml-rail');
  if (rail) rail.addEventListener('click', () => setYamlCollapsed(false));
}
wireYamlResize();
