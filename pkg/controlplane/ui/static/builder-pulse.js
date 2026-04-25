// Run-live pulse animation + SVG defs for arrow markers + comet gradient.
//
// Extracted from builder.js to keep the main file under the size cap. The
// module exposes four globals consumed by builder.js:
//
//   arrowDefs(svgNS)   — <defs> builder for the connections SVG
//   startPulseAnim()   — begin the running animation loop
//   stopPulseAnim()    — clear it + remove any lingering overlay nodes
//   firePulse(idx)     — visual step: comet along edge[idx] + burst + card halo
//
// State shared with builder.js (window.state, window.orderedSteps) is read
// inside firePulse(), so this file MUST load after builder.js attaches those
// references — see builder.html script order.

function arrowDefs(svgNS) {
  const defs = document.createElementNS(svgNS, 'defs');
  const mk = (id, cls) => {
    const m = document.createElementNS(svgNS, 'marker');
    m.setAttribute('id', id);
    m.setAttribute('viewBox', '0 0 10 10');
    m.setAttribute('refX', '6');
    m.setAttribute('refY', '5');
    m.setAttribute('markerWidth', '7');
    m.setAttribute('markerHeight', '7');
    m.setAttribute('orient', 'auto');
    const p = document.createElementNS(svgNS, 'path');
    p.setAttribute('d', 'M 0 0 L 8 5 L 0 10 z');
    p.setAttribute('class', cls);
    m.appendChild(p);
    return m;
  };
  defs.appendChild(mk('arrow-default', 'arrow-fill-default'));
  defs.appendChild(mk('arrow-pass',    'arrow-fill-pass'));
  defs.appendChild(mk('arrow-fail',    'arrow-fill-fail'));
  defs.appendChild(mk('arrow-warn',    'arrow-fill-warn'));

  // Live-run pulse gradient (green comet travelling along the edge) + glow
  // filter. Used only while a run is in flight; idle connections ignore them.
  const grad = document.createElementNS(svgNS, 'linearGradient');
  grad.setAttribute('id', 'pulse');
  grad.setAttribute('x1', '0'); grad.setAttribute('y1', '0');
  grad.setAttribute('x2', '1'); grad.setAttribute('y2', '0');
  const stops = [
    ['0%',   'var(--cta-glow)',              '0'],
    ['45%',  'var(--cta-glow)',              '1'],
    ['55%',  'oklch(0.92 0.15 150)',         '1'],
    ['100%', 'var(--cta-glow)',              '0'],
  ];
  for (const [off, color, op] of stops) {
    const s = document.createElementNS(svgNS, 'stop');
    s.setAttribute('offset', off);
    s.setAttribute('stop-color', color);
    s.setAttribute('stop-opacity', op);
    grad.appendChild(s);
  }
  defs.appendChild(grad);

  const filt = document.createElementNS(svgNS, 'filter');
  filt.setAttribute('id', 'glow');
  filt.setAttribute('x', '-50%'); filt.setAttribute('y', '-50%');
  filt.setAttribute('width', '200%'); filt.setAttribute('height', '200%');
  const blur = document.createElementNS(svgNS, 'feGaussianBlur');
  blur.setAttribute('stdDeviation', '2.2');
  blur.setAttribute('result', 'b');
  filt.appendChild(blur);
  const merge = document.createElementNS(svgNS, 'feMerge');
  const m1 = document.createElementNS(svgNS, 'feMergeNode'); m1.setAttribute('in', 'b');
  const m2 = document.createElementNS(svgNS, 'feMergeNode'); m2.setAttribute('in', 'SourceGraphic');
  merge.appendChild(m1); merge.appendChild(m2);
  filt.appendChild(merge);
  defs.appendChild(filt);

  return defs;
}

let pulseTimer = null;
let pulseCursor = 0;

function startPulseAnim() {
  stopPulseAnim();
  pulseCursor = 0;
  firePulse(pulseCursor);
  pulseTimer = setInterval(() => {
    pulseCursor += 1;
    firePulse(pulseCursor);
  }, 900);
}

function stopPulseAnim() {
  if (pulseTimer != null) { clearInterval(pulseTimer); pulseTimer = null; }
  clearPulseLayer();
  document.querySelectorAll('.card.firing').forEach(c => c.classList.remove('firing'));
}

function clearPulseLayer() {
  document.querySelectorAll('svg.connections .pulse-layer').forEach(n => n.remove());
}

function firePulse(idx) {
  const svg = document.querySelector('svg.connections');
  if (!svg) return;
  const edges = svg.querySelectorAll('path.connection:not(.check-link)');
  if (edges.length === 0) return;
  const edge = edges[idx % edges.length];
  if (!edge) return;

  clearPulseLayer();
  const svgNS = 'http://www.w3.org/2000/svg';

  // Traveling comet along the edge.
  const trail = document.createElementNS(svgNS, 'path');
  trail.setAttribute('d', edge.getAttribute('d'));
  trail.setAttribute('class', 'pulse-layer pulse-trail');
  trail.setAttribute('stroke', 'url(#pulse)');
  trail.setAttribute('stroke-width', '2.4');
  trail.setAttribute('fill', 'none');
  trail.setAttribute('stroke-linecap', 'round');
  trail.setAttribute('stroke-dasharray', '60 9999');
  trail.setAttribute('filter', 'url(#glow)');
  svg.appendChild(trail);

  // Fire-burst circle at the target (top of next card).
  const tx = parseFloat(edge.dataset.tgtX);
  const ty = parseFloat(edge.dataset.tgtY);
  if (!isNaN(tx) && !isNaN(ty)) {
    const burst = document.createElementNS(svgNS, 'circle');
    burst.setAttribute('class', 'pulse-layer fire-burst');
    burst.setAttribute('cx', String(tx));
    burst.setAttribute('cy', String(ty));
    burst.setAttribute('r', '3');
    burst.setAttribute('fill', 'var(--cta-glow)');
    burst.setAttribute('filter', 'url(#glow)');
    svg.appendChild(burst);
  }

  // Synchronised halo on the source + target cards so the whole row reads
  // as "this step is running right now". We look up the cards by resolving
  // the edge's Y-sorted indices back to step IDs. Halo is removed by the
  // next firePulse call (single .firing at a time) or by stopPulseAnim.
  document.querySelectorAll('.card.firing').forEach(c => c.classList.remove('firing'));
  const ordered = window.orderedSteps(window.state);
  const edgeN = parseInt(edge.dataset.edgeIdx || '0', 10);
  // Edge N links ordered[srcN] → ordered[srcN+1]; but we skip pairs where
  // the next step isn't below, so walk ordered and count kept edges.
  let kept = 0, srcStep = null;
  for (let i = 0; i < ordered.length - 1; i++) {
    const a = ordered[i], b = ordered[i + 1];
    if (b.y <= a.y) continue;
    if (kept === edgeN) { srcStep = a; break; }
    kept++;
  }
  if (srcStep) {
    const srcEl = document.querySelector(`.card[data-id="${srcStep.id}"]`);
    if (srcEl) srcEl.classList.add('firing');
  }
}
