// ================== API ==================
// По умолчанию тот же origin, что и фронт. Можно переопределить через ?api=http://...
const params = new URLSearchParams(location.search);
const API_BASE = params.get('api') || '';
let CURRENT_CLUSTER = params.get('cluster') || '';
document.getElementById('apiUrl').textContent = API_BASE || location.origin;

async function api(path) {
  let url = API_BASE + path;
  if (CURRENT_CLUSTER) {
    url += (path.includes('?') ? '&' : '?') + 'cluster=' + encodeURIComponent(CURRENT_CLUSTER);
  }
  const r = await fetch(url, { headers: { 'Accept': 'application/json' } });
  if (!r.ok) throw new Error(`${path}: ${r.status}`);
  return r.json();
}

function setHealth(state, text) {
  const el = document.getElementById('health');
  el.className = 'health ' + state;
  document.getElementById('healthText').textContent = text;
}

// ================== RENDER ==================

function fmt(n) { return (n ?? 0).toLocaleString('en-US'); }
function barClass(v) { return v >= 80 ? 'high' : v >= 60 ? 'mid' : ''; }
function barColor(v) { return v >= 80 ? 'var(--err)' : v >= 60 ? 'var(--warn)' : 'var(--accent)'; }

function renderStats(o) {
  const items = [
    { label: 'nodes',       value: o.nodes },
    { label: 'pods',        value: fmt(o.pods) },
    { label: 'deployments', value: o.deployments },
    { label: 'services',    value: o.services },
    { label: 'cpu used',    value: o.cpuUsed + '%', bar: o.cpuUsed },
    { label: 'mem used',    value: o.memUsed + '%', bar: o.memUsed },
  ];
  document.getElementById('stats').innerHTML = items.map(i => `
    <div class="stat">
      <div class="stat-label">${i.label}</div>
      <div class="stat-value">${i.value}</div>
      ${i.bar != null ? `<div class="stat-bar" style="width: ${i.bar}%; background: ${barColor(i.bar)};"></div>` : ''}
    </div>
  `).join('');
}

function renderNodes(nodes) {
  document.getElementById('nodesCount').textContent = nodes.length + ' total';
  if (!nodes.length) {
    document.getElementById('nodesList').innerHTML = '<div class="empty">no nodes</div>';
    return;
  }
  document.getElementById('nodesList').innerHTML = nodes.map(n => {
    const metaParts = [];
    if (n.kernel)  metaParts.push(escapeHtml(n.kernel));
    if (n.os)      metaParts.push(escapeHtml(n.os));
    if (n.kubelet) metaParts.push('kubelet ' + escapeHtml(n.kubelet));
    const meta = metaParts.length
      ? `<div class="node-meta">${metaParts.join('<span class="sep">·</span>')}</div>`
      : '';
    return `
    <div class="node-row">
      <span class="node-status ${n.status}"></span>
      <span>
        <span class="node-name">${escapeHtml(n.name)}</span><span class="node-role">${escapeHtml(n.role)}</span>
        ${meta}
      </span>
      <div>
        <div class="mini-bar ${barClass(n.cpu)}" style="--v: ${n.cpu}%"></div>
        <div class="mini-val">${n.cpu.toFixed(0)}%</div>
      </div>
      <div>
        <div class="mini-bar ${barClass(n.mem)}" style="--v: ${n.mem}%"></div>
        <div class="mini-val">${n.mem.toFixed(0)}%</div>
      </div>
      <span class="mini-val">${n.pods}</span>
      <span class="mini-val">${n.age}</span>
    </div>`;
  }).join('');
}

function renderDonut(p) {
  const total = p.running + p.pending + p.succeeded + p.failed + p.unknown;
  document.getElementById('podsTotal').textContent = total + ' pods';

  const segments = [
    { label: 'Running',   color: 'var(--accent)', value: p.running },
    { label: 'Pending',   color: 'var(--warn)',   value: p.pending },
    { label: 'Succeeded', color: 'var(--info)',   value: p.succeeded },
    { label: 'Failed',    color: 'var(--err)',    value: p.failed },
    { label: 'Unknown',   color: 'var(--text-3)', value: p.unknown },
  ];

  const C = 2 * Math.PI * 15.9;
  let offset = 0;
  const svg = document.getElementById('donut');
  svg.innerHTML = `<circle cx="21" cy="21" r="15.9" fill="transparent" stroke="var(--bg-3)" stroke-width="3"></circle>`;
  if (total > 0) {
    segments.forEach(seg => {
      if (seg.value === 0) return;
      const dash = (seg.value / total) * C;
      svg.innerHTML += `<circle cx="21" cy="21" r="15.9" fill="transparent"
        stroke="${seg.color}" stroke-width="3"
        stroke-dasharray="${dash} ${C - dash}"
        stroke-dashoffset="${-offset}"
        transform="rotate(-90 21 21)"></circle>`;
      offset += dash;
    });
  }
  svg.innerHTML += `<text x="21" y="22" text-anchor="middle" fill="var(--text-1)" font-family="JetBrains Mono" font-size="6" font-weight="600">${total}</text>
                    <text x="21" y="27" text-anchor="middle" fill="var(--text-3)" font-family="JetBrains Mono" font-size="2.5" letter-spacing="0.3">TOTAL</text>`;

  document.getElementById('donutLegend').innerHTML = segments.map(s => `
    <div class="legend-row">
      <div class="legend-left">
        <span class="legend-swatch" style="background: ${s.color};"></span>
        <span class="legend-label">${s.label}</span>
      </div>
      <span class="legend-value">${s.value}</span>
    </div>
  `).join('');
}

function renderChart(data) {
  const cpu = (data.cpu?.points || []).map(p => p[1]);
  const mem = (data.mem?.points || []).map(p => p[1]);
  const svg = document.getElementById('chart');

  if (cpu.length === 0 && mem.length === 0) {
    svg.innerHTML = `<text x="300" y="80" text-anchor="middle" fill="var(--text-3)" font-family="JetBrains Mono" font-size="10">no metrics yet</text>`;
    return;
  }

  const W = 600, H = 160, P = 10;
  const toPath = (arr) => arr.map((v, i) => {
    const x = P + (i / Math.max(1, arr.length - 1)) * (W - 2*P);
    const y = H - P - (Math.max(0, Math.min(100, v)) / 100) * (H - 2*P);
    return (i === 0 ? 'M' : 'L') + x.toFixed(1) + ',' + y.toFixed(1);
  }).join(' ');
  const toArea = (arr) => arr.length ? toPath(arr) + ` L${W-P},${H-P} L${P},${H-P} Z` : '';

  let grid = '';
  for (let i = 0; i <= 4; i++) {
    const y = P + i * ((H - 2*P) / 4);
    grid += `<line x1="${P}" y1="${y}" x2="${W-P}" y2="${y}" stroke="var(--border)" stroke-width="0.5" stroke-dasharray="2,3"/>`;
  }

  svg.innerHTML = `
    <defs>
      <linearGradient id="cpuGrad" x1="0" x2="0" y1="0" y2="1">
        <stop offset="0%" stop-color="var(--accent)" stop-opacity="0.25"/>
        <stop offset="100%" stop-color="var(--accent)" stop-opacity="0"/>
      </linearGradient>
      <linearGradient id="memGrad" x1="0" x2="0" y1="0" y2="1">
        <stop offset="0%" stop-color="var(--info)" stop-opacity="0.2"/>
        <stop offset="100%" stop-color="var(--info)" stop-opacity="0"/>
      </linearGradient>
    </defs>
    ${grid}
    ${cpu.length ? `<path d="${toArea(cpu)}" fill="url(#cpuGrad)"/><path d="${toPath(cpu)}" fill="none" stroke="var(--accent)" stroke-width="1.5"/>` : ''}
    ${mem.length ? `<path d="${toArea(mem)}" fill="url(#memGrad)"/><path d="${toPath(mem)}" fill="none" stroke="var(--info)" stroke-width="1.5"/>` : ''}
  `;
}

function renderEvents(events) {
  document.getElementById('eventsCount').textContent = events.length + ' recent';
  if (!events.length) {
    document.getElementById('eventsList').innerHTML = '<div class="empty">no events</div>';
    return;
  }
  document.getElementById('eventsList').innerHTML = events.map(e => `
    <div class="event">
      <span class="event-time">${escapeHtml(e.t)}</span>
      <span class="event-type ${e.type}">${escapeHtml(e.type.slice(0,4))}</span>
      <span class="event-msg">${e.msg}</span>
    </div>
  `).join('');
}

function renderWorkloads(wls) {
  document.getElementById('wlCount').textContent = wls.length + ' total';
  if (!wls.length) {
    document.getElementById('workloadsList').innerHTML = '<div class="empty">no workloads</div>';
    return;
  }
  document.getElementById('workloadsList').innerHTML = wls.map(w => {
    const readyClass = w.ready === w.replicas ? 'ok' : w.ready === 0 ? 'err' : 'partial';
    return `
      <div class="workload-row">
        <span><span class="wl-name">${escapeHtml(w.name)}</span><br><span class="wl-ns">${escapeHtml(w.ns)}</span></span>
        <span><span class="badge ${w.kind}">${escapeHtml(w.kind)}</span></span>
        <span class="wl-replicas">${w.replicas}</span>
        <span class="wl-ready ${readyClass}">${w.ready}/${w.replicas}</span>
        <span class="wl-replicas">${escapeHtml(w.age)}</span>
      </div>
    `;
  }).join('');
}

function renderNamespaces(nss) {
  document.getElementById('nsCount').textContent = nss.length + ' total';
  if (!nss.length) {
    document.getElementById('nsGrid').innerHTML = '<div class="empty">no namespaces</div>';
    return;
  }
  document.getElementById('nsGrid').innerHTML = nss.map(ns => `
    <div class="ns-cell">
      <div class="ns-name">${escapeHtml(ns.name)}</div>
      <div class="ns-stat">
        <span class="ok">${ns.pods - ns.bad} ok</span>
        ${ns.bad > 0 ? `<span class="err">${ns.bad} bad</span>` : ''}
      </div>
    </div>
  `).join('');
}

function escapeHtml(s) {
  if (s == null) return '';
  return String(s).replace(/[&<>"']/g, c => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
  }[c]));
}

// ================== LOADER ==================

async function refresh() {
  // health отдельно, чтобы дашборд не "моргал" при временных ошибках
  try {
    const h = await api('/api/health');
    setHealth(h.k8s ? 'ok' : 'err', h.k8s ? (h.metrics ? 'K8S + METRICS OK' : 'K8S OK · METRICS ?') : 'K8S DOWN');
  } catch (e) {
    setHealth('err', 'BACKEND DOWN');
  }

  const tasks = [
    ['/api/overview',        renderStats],
    ['/api/nodes',           renderNodes],
    ['/api/pods/status',     renderDonut],
    ['/api/metrics/cluster', renderChart],
    ['/api/events?limit=20', renderEvents],
    ['/api/workloads',       renderWorkloads],
    ['/api/namespaces',      renderNamespaces],
  ];
  await Promise.all(tasks.map(async ([path, fn]) => {
    try {
      const data = await api(path);
      fn(data);
    } catch (e) {
      console.error(path, e);
    }
  }));

  const d = new Date();
  document.getElementById('lastUpdate').textContent =
    `updated ${String(d.getHours()).padStart(2,'0')}:${String(d.getMinutes()).padStart(2,'0')}:${String(d.getSeconds()).padStart(2,'0')}`;
}

// k8s OK + prom OK = ok; k8s OK + prom DOWN = warn; k8s DOWN = down
function clusterState(c) {
  if (!c.k8s) return 'down';
  if (!c.metrics) return 'warn';
  return 'ok';
}

async function loadClusters() {
  const wrap = document.getElementById('clusterTabs');
  let list;
  try {
    list = await api('/api/clusters');
  } catch (e) {
    console.error('clusters', e);
    return;
  }
  if (!Array.isArray(list) || list.length === 0) return;

  if (!CURRENT_CLUSTER || !list.some(c => c.name === CURRENT_CLUSTER)) {
    CURRENT_CLUSTER = list[0].name;
  }
  updateClusterViewTab(list);

  wrap.innerHTML = list.map(c => {
    const cls = ['cluster-tab', clusterState(c)];
    if (c.name === CURRENT_CLUSTER) cls.push('active');
    return `<button class="${cls.join(' ')}" data-name="${c.name}" title="k8s: ${c.k8s ? 'OK' : 'DOWN'} · metrics: ${c.metrics ? 'OK' : 'DOWN'}"><span class="tab-dot"></span>${c.name}</button>`;
  }).join('');

  wrap.querySelectorAll('.cluster-tab').forEach(btn => {
    btn.addEventListener('click', () => {
      const name = btn.dataset.name;
      if (name === CURRENT_CLUSTER) return;
      CURRENT_CLUSTER = name;
      const u = new URL(location);
      u.searchParams.set('cluster', name);
      history.replaceState(null, '', u);
      wrap.querySelectorAll('.cluster-tab').forEach(x =>
        x.classList.toggle('active', x.dataset.name === name));
      refresh();
    });
  });
}

function updateClusterViewTab(list) {
  const tab = document.querySelector('.view-tab[data-view="cluster"]');
  if (!tab || !list.length) return;
  const current = list.find(c => c.name === CURRENT_CLUSTER) || list[0];
  const state = clusterState(current); // 'ok' | 'warn' | 'down'
  tab.classList.remove('ok', 'warn', 'down');
  tab.classList.add(state);
}

// Лёгкое обновление: только классы статусов, без перерендера/перебиндинга.
async function refreshClusterStatus() {
  let list;
  try {
    list = await api('/api/clusters');
  } catch { return; }
  if (!Array.isArray(list)) return;
  for (const c of list) {
    const btn = document.querySelector(`.cluster-tab[data-name="${c.name}"]`);
    if (!btn) continue;
    btn.classList.remove('ok', 'warn', 'down');
    btn.classList.add(clusterState(c));
    btn.title = `k8s: ${c.k8s ? 'OK' : 'DOWN'} · metrics: ${c.metrics ? 'OK' : 'DOWN'}`;
  }
  updateClusterViewTab(list);
}

// ================== SERVERS view ==================

const SERVERS_EXPANDED = new Set();

function serverWorstClass(s) {
  return s.worst || (s.up ? 'ok' : 'down');
}

function renderServers(list) {
  const badge = document.getElementById('serversBadge');
  const tab   = document.querySelector('.view-tab[data-view="servers"]');
  const count = document.getElementById('serversCount');

  if (!Array.isArray(list) || list.length === 0) {
    badge.style.display = 'none';
    count.textContent = '—';
    document.getElementById('serversList').innerHTML =
      '<div class="empty">no servers configured — use -servers flag</div>';
    return;
  }

  const ranks = { ok: 1, warn: 2, crit: 3, down: 3 };
  let worst = 'ok', badCount = 0;
  for (const s of list) {
    const w = serverWorstClass(s);
    if ((ranks[w] || 0) > (ranks[worst] || 0)) worst = w;
    if (w !== 'ok') badCount++;
  }

  tab.classList.remove('ok', 'warn', 'crit', 'down');
  tab.classList.add(worst);
  if (badCount > 0) { badge.style.display = ''; badge.textContent = badCount; }
  else badge.style.display = 'none';
  count.textContent = list.length + ' total';

  const panel = document.getElementById('serversList');
  // Diff-update: если набор имён не поменялся — апдейтим только статусы и алерты,
  // не пересобирая DOM. Иначе перерисовка целиком.
  const existingNames = Array.from(panel.querySelectorAll('.servers-row'))
    .map(r => r.dataset.name);
  const newNames = list.map(s => s.name);
  const sameSet = existingNames.length === newNames.length &&
                  existingNames.every((n, i) => n === newNames[i]);

  if (!sameSet) {
    panel.innerHTML = list.map(s => `
      <div class="servers-row ${SERVERS_EXPANDED.has(s.name) ? 'expanded' : ''}" data-name="${escapeHtml(s.name)}">
        <div class="head">
          <span class="srv-dot"></span>
          <span class="name">${escapeHtml(s.name)}</span>
          <span class="srv-error" style="color:var(--text-3);font-size:10px"></span>
          <a href="#/server/${encodeURIComponent(s.name)}" class="srv-open"
             onclick="event.stopPropagation()"
             style="color:var(--accent);text-decoration:none;font-size:10px;
                    font-family:'JetBrains Mono',monospace;text-transform:uppercase;
                    letter-spacing:0.06em">OPEN ↗</a>
        </div>
        <div class="url-meta">${escapeHtml(s.url)}</div>
        <div class="alerts"></div>
      </div>`).join('');

    panel.querySelectorAll('.servers-row').forEach(row => {
      row.addEventListener('click', () => {
        const name = row.dataset.name;
        if (SERVERS_EXPANDED.has(name)) SERVERS_EXPANDED.delete(name);
        else SERVERS_EXPANDED.add(name);
        row.classList.toggle('expanded');
      });
    });
  }

  // апдейт состояний и алертов на месте
  for (const s of list) {
    const row = panel.querySelector(`.servers-row[data-name="${CSS.escape(s.name)}"]`);
    if (!row) continue;
    const dot = row.querySelector('.srv-dot');
    dot.className = 'srv-dot ' + serverWorstClass(s);
    row.querySelector('.srv-error').textContent = s.up ? '' : (s.error || 'down');
    row.querySelector('.alerts').innerHTML = (s.alerts || []).map(a => `
      <div class="alert-item">
        <span class="sev ${a.severity}">${a.severity}</span>
        <div>
          <div class="msg">${escapeHtml(a.msg || '')}</div>
          <div class="rule">${escapeHtml(a.rule)}</div>
        </div>
      </div>`).join('');
  }
}

async function refreshServers() {
  let list;
  try { list = await api('/api/servers'); } catch { return; }
  renderServers(list);
}

// ================== SERVER DETAIL ==================

function fmtBytes(n) {
  if (!n || n < 0) return '—';
  const u = ['B','KiB','MiB','GiB','TiB','PiB'];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return n.toFixed(i === 0 ? 0 : 1) + ' ' + u[i];
}

function fmtUptime(sec) {
  if (!sec || sec <= 0) return '—';
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

function pct(used, total) {
  if (!total || total <= 0) return 0;
  return Math.round((used / total) * 1000) / 10;
}

function renderServerDetail(d) {
  const body = document.getElementById('serverDetailBody');
  if (!d) {
    body.innerHTML = '<div class="empty err">server not found</div>';
    return;
  }

  const memUsed = d.mem.totalBytes - d.mem.availableBytes;
  const swapUsed = d.mem.swapTotalBytes - d.mem.swapFreeBytes;

  const downBanner = d.up ? '' : `
    <div class="down-banner">⚠ NodeExporter on ${escapeHtml(d.url)} is unreachable
      ${d.error ? '— ' + escapeHtml(d.error) : ''}</div>`;

  const fsRows = (d.filesystems || []).map(fs => `
    <div class="fs-row">
      <div class="mp">${escapeHtml(fs.mountpoint)} <span class="fst">${escapeHtml(fs.fstype)}</span></div>
      <div class="mini-val">${fs.usedPct.toFixed(0)}%</div>
      <div class="mini-val">${fmtBytes(fs.sizeBytes - fs.availBytes)} / ${fmtBytes(fs.sizeBytes)}</div>
      <div class="mini-val">${fs.inodesUsedPct.toFixed(0)}% inode</div>
    </div>`).join('') || '<div class="empty">no filesystems</div>';

  const netRows = (d.networks || []).map(n => `
    <div class="net-row">
      <div>${escapeHtml(n.iface)}</div>
      <div class="mini-val">↓ ${fmtBytes(n.rxBytes)}</div>
      <div class="mini-val">↑ ${fmtBytes(n.txBytes)}</div>
    </div>`).join('') || '<div class="empty">no interfaces</div>';

  const alertRows = (d.alerts || []).map(a => `
    <div class="alert-detail">
      <span class="sev ${a.severity}">${a.severity}</span>
      <span class="rule">${escapeHtml(a.rule)}</span>
      <span class="msg">${escapeHtml(a.msg || '')}</span>
    </div>`).join('') || '<div class="empty">no alerts</div>';

  body.innerHTML = `
    <a href="#" class="back-link" id="serverBack">← back to servers</a>
    <div class="detail-head">
      <h1>${escapeHtml(d.name)}</h1>
      <span class="url">${escapeHtml(d.url)}</span>
    </div>
    <div class="detail-meta">
      ${escapeHtml(d.uname.sysname || '')} ${escapeHtml(d.uname.release || '')}
      <span class="sep">·</span> ${escapeHtml(d.uname.machine || '')}
      <span class="sep">·</span> uptime ${fmtUptime(d.uptimeSeconds)}
      <span class="sep">·</span> ${d.cpu.cores} cores
      <span class="sep">·</span> load ${d.cpu.load1.toFixed(2)} / ${d.cpu.load5.toFixed(2)} / ${d.cpu.load15.toFixed(2)}
    </div>
    ${downBanner}
    <div class="grid-detail">
      <div class="panel">
        <div class="panel-head"><span>Memory</span></div>
        <div class="panel-body">
          <div class="kv-row"><span class="k">total</span><span class="v">${fmtBytes(d.mem.totalBytes)}</span></div>
          <div class="kv-row"><span class="k">used</span><span class="v">${fmtBytes(memUsed)} (${pct(memUsed, d.mem.totalBytes).toFixed(0)}%)</span></div>
          <div class="kv-row"><span class="k">available</span><span class="v">${fmtBytes(d.mem.availableBytes)}</span></div>
          <div class="kv-row"><span class="k">free</span><span class="v">${fmtBytes(d.mem.freeBytes)}</span></div>
          <div class="kv-row"><span class="k">buffers</span><span class="v">${fmtBytes(d.mem.buffersBytes)}</span></div>
          <div class="kv-row"><span class="k">cached</span><span class="v">${fmtBytes(d.mem.cachedBytes)}</span></div>
          <div class="kv-row"><span class="k">swap</span><span class="v">${fmtBytes(swapUsed)} / ${fmtBytes(d.mem.swapTotalBytes)} used</span></div>
        </div>
      </div>
      <div class="panel">
        <div class="panel-head"><span>Filesystems</span></div>
        <div class="panel-body">
          <div class="fs-row header"><span>Mount / fstype</span><span>Used</span><span>Size</span><span>Inodes</span></div>
          ${fsRows}
        </div>
      </div>
      <div class="panel">
        <div class="panel-head"><span>Network</span></div>
        <div class="panel-body">
          <div class="net-row header"><span>Interface</span><span>RX</span><span>TX</span></div>
          ${netRows}
        </div>
      </div>
      <div class="panel">
        <div class="panel-head"><span>Alerts</span></div>
        <div class="panel-body">${alertRows}</div>
      </div>
    </div>`;

  document.getElementById('serverBack').addEventListener('click', e => {
    e.preventDefault();
    location.hash = '';
    switchView('servers');
  });
}

async function loadServerDetail(name) {
  let d;
  try { d = await api('/api/servers/detail?name=' + encodeURIComponent(name)); }
  catch { renderServerDetail(null); return; }
  renderServerDetail(d);
}

// ================== VIEW SWITCHING ==================

let CURRENT_VIEW = 'cluster';
let CURRENT_DETAIL_SERVER = '';

function switchView(view) {
  CURRENT_VIEW = view;
  localStorage.setItem('current-view', view);
  document.getElementById('mainView').style.display          = view === 'cluster' ? '' : 'none';
  document.getElementById('runnersView').style.display       = view === 'runners' ? '' : 'none';
  document.getElementById('serversView').style.display       = view === 'servers' ? '' : 'none';
  document.getElementById('serverDetailView').style.display  = view === 'serverDetail' ? '' : 'none';
  document.getElementById('toolsView').style.display         = view === 'tools' ? '' : 'none';
  document.getElementById('clusterSubNav').style.display     = view === 'cluster' ? '' : 'none';
  document.querySelectorAll('.view-tab').forEach(t =>
    t.classList.toggle('active', t.dataset.view === view ||
      (view === 'serverDetail' && t.dataset.view === 'servers')));
  if (view === 'runners') refreshRunners();
  if (view === 'servers') refreshServers();
  if (view === 'tools')   refreshTools();
  if (view === 'serverDetail' && CURRENT_DETAIL_SERVER) loadServerDetail(CURRENT_DETAIL_SERVER);
}

function applyHashRoute() {
  const m = location.hash.match(/^#\/server\/(.+)$/);
  if (m) {
    CURRENT_DETAIL_SERVER = decodeURIComponent(m[1]);
    switchView('serverDetail');
  } else if (CURRENT_VIEW === 'serverDetail') {
    switchView('servers');
  }
}
window.addEventListener('hashchange', applyHashRoute);

document.querySelectorAll('.view-tab').forEach(btn => {
  btn.addEventListener('click', () => {
    if (location.hash.startsWith('#/server/')) location.hash = '';
    switchView(btn.dataset.view);
  });
});

// ================== RUNNERS ==================

function relTime(iso) {
  if (!iso) return '—';
  const ms = Date.now() - new Date(iso).getTime();
  if (ms < 0) return 'just now';
  const s = Math.floor(ms / 1000);
  if (s < 60)  return s + 's ago';
  const m = Math.floor(s / 60);
  if (m < 60)  return m + 'm ago';
  const h = Math.floor(m / 60);
  if (h < 24)  return h + 'h ago';
  return Math.floor(h / 24) + 'd ago';
}

function runnerTypeName(t) {
  return { instance_type: 'shared', group_type: 'group', project_type: 'project' }[t] || t || '—';
}

function runnerPriority(r) {
  if (r.status === 'online' && !r.paused) return 1;
  if (r.paused) return 2;
  if (!r.contactedAt) return 5;
  const ageDays = (Date.now() - new Date(r.contactedAt)) / 86400000;
  return ageDays < 7 ? 3 : 4;
}

function lastSeenClass(iso) {
  if (!iso) return 'ls-never';
  const ms = Date.now() - new Date(iso);
  if (ms < 60000)        return 'ls-fresh';   // <1m green
  if (ms < 600000)       return 'ls-recent';  // <10m yellow
  if (ms < 3600000)      return 'ls-ok';      // <1h dim
  return 'ls-old';                             // >1h red
}

function updateRunnersViewTab(runners) {
  const tab = document.querySelector('.view-tab[data-view="runners"]');
  if (!tab) return;
  tab.classList.remove('ok', 'warn', 'down');
  if (!runners.length) return;
  const dead   = runners.filter(r => r.status !== 'online').length;
  const paused = runners.filter(r => r.paused).length;
  if (dead === runners.length) tab.classList.add('down');
  else if (paused > 0)         tab.classList.add('warn');
  else                         tab.classList.add('ok');
}

function renderRunnerSummary(runners) {
  const el = document.getElementById('runnerSummary');
  if (!el || !runners.length) { if (el) el.innerHTML = ''; return; }

  const online  = runners.filter(r => r.status === 'online' && !r.paused).length;
  const paused  = runners.filter(r => r.paused).length;
  const offline = runners.filter(r => r.status !== 'online').length;
  const stale   = runners.filter(r => {
    if (!r.contactedAt || r.status === 'online') return false;
    return (Date.now() - new Date(r.contactedAt)) > 86400000;
  }).length;
  const never   = runners.filter(r => !r.contactedAt).length;
  const shared  = runners.filter(r => r.runnerType === 'instance_type').length;
  const group   = runners.filter(r => r.runnerType === 'group_type').length;
  const project = runners.filter(r => r.runnerType === 'project_type').length;

  const stat = (dot, val, label, cls = '') =>
    `<div class="rs-stat ${cls}">
      ${dot ? `<span class="runner-dot ${dot}"></span>` : ''}
      <span class="rs-val">${val}</span>
      <span class="rs-label">${label}</span>
    </div>`;

  el.innerHTML = `
    <div class="runner-summary-bar">
      ${stat('online',  online,  'online')}
      ${paused  ? stat('paused', paused,  'paused')      : ''}
      ${stat('dead',   offline, 'offline')}
      ${stale   ? stat('', stale,   'stale >24h', 'rs-muted') : ''}
      ${never   ? stat('', never,   'never seen',  'rs-muted') : ''}
      <div class="rs-divider"></div>
      ${shared  ? stat('', shared,  'shared',  'rs-muted') : ''}
      ${group   ? stat('', group,   'group',   'rs-muted') : ''}
      ${project ? stat('', project, 'project', 'rs-muted') : ''}
    </div>`;
}

function renderRunners(runners) {
  updateRunnersViewTab(runners);
  document.getElementById('runnersCount').textContent = runners.length + ' total';
  renderRunnerSummary(runners);

  if (!runners.length) {
    document.getElementById('runnersList').innerHTML =
      '<div class="empty">no runners — configure GitLab with -gitlab-url and -gitlab-token</div>';
    return;
  }

  const sorted = [...runners].sort((a, b) => {
    const pd = runnerPriority(a) - runnerPriority(b);
    if (pd !== 0) return pd;
    if (!a.contactedAt && !b.contactedAt) return 0;
    if (!a.contactedAt) return 1;
    if (!b.contactedAt) return -1;
    return new Date(b.contactedAt) - new Date(a.contactedAt);
  });

  const rows = sorted.map(r => {
    const dotCls   = r.paused ? 'paused' : (r.status === 'online' ? 'online' : 'dead');
    const tip      = r.contactedAt ? `last heartbeat ${relTime(r.contactedAt)}` : 'never contacted';
    const typeCls  = escapeHtml(r.runnerType || '');
    const clickable = r.webURL ? ' clickable' : '';

    const tags = [...(r.tags || [])].sort().map(t => `<span class="runner-tag">${escapeHtml(t)}</span>`).join('');

    const badges = [];
    if (!r.description)  badges.push('<span class="runner-badge unnamed">unnamed</span>');
    if (!r.contactedAt)  badges.push('<span class="runner-badge never">never seen</span>');
    if (r.paused)        badges.push('<span class="runner-paused">paused</span>');
    const executor = r.executor ? `<span class="runner-executor">${escapeHtml(r.executor)}</span>` : '';
    const hostname = r.hostname ? `<span class="runner-hostname">${escapeHtml(r.hostname)}</span>` : '';

    return `
    <div class="runner-row${clickable}" data-url="${escapeHtml(r.webURL || '')}">
      <span class="runner-dot ${dotCls}" title="${tip}"></span>
      <span class="runner-name-cell">
        <span class="runner-desc">${escapeHtml(r.description || '—')}</span>
        <span class="runner-name-meta">${hostname}${executor}${badges.join('')}</span>
      </span>
      <span class="runner-tags">${tags || '<span class="runner-no-tags">—</span>'}</span>
      <span><span class="runner-type-badge ${typeCls}">${escapeHtml(runnerTypeName(r.runnerType))}</span></span>
      <span class="runner-ago ${lastSeenClass(r.contactedAt)}">${relTime(r.contactedAt)}</span>
      <span class="runner-id">${r.id}</span>
    </div>`;
  }).join('');

  const list = document.getElementById('runnersList');
  list.innerHTML = rows;
  list.querySelectorAll('.runner-row.clickable').forEach(row =>
    row.addEventListener('click', () => window.open(row.dataset.url, '_blank')));
}

async function refreshRunners() {
  try {
    const resp = await fetch(API_BASE + '/api/runners', { headers: { 'Accept': 'application/json' } });
    if (!resp.ok) {
      document.getElementById('runnersList').innerHTML =
        `<div class="empty err">runners API error ${resp.status} — check server log</div>`;
      return;
    }
    renderRunners(await resp.json());
  } catch (e) {
    console.error(e);
    document.getElementById('runnersList').innerHTML =
      '<div class="empty err">cannot reach backend</div>';
  }
}

// ================== RUNNERS COLLAPSE ==================

function initRunnersCollapse() {
  const panel = document.getElementById('runnersPanel');
  if (!panel) return;
  if (localStorage.getItem('runners-collapsed') === '1') panel.classList.add('collapsed');
  panel.querySelector('.panel-head').addEventListener('click', () => {
    const c = panel.classList.toggle('collapsed');
    localStorage.setItem('runners-collapsed', c ? '1' : '0');
  });
}

// ================== PIPELINES ==================

const PL_STATUS_CLASS = {
  running: 'running', pending: 'pending', created: 'pending',
  waiting_for_resource: 'pending', preparing: 'pending', scheduled: 'pending',
  success: 'ok', failed: 'down', canceled: 'canceled', skipped: 'skipped',
};

const PL_CAN_RETRY  = new Set(['failed', 'canceled', 'success', 'skipped']);
const PL_CAN_CANCEL = new Set(['running', 'pending', 'created', 'waiting_for_resource', 'preparing', 'scheduled']);

// pinned: Set of fullPaths. Empty = show all. Non-empty = show only these.
let plPinned = (() => {
  try { const s = localStorage.getItem('pl-pinned'); return s ? new Set(JSON.parse(s)) : new Set(); }
  catch { return new Set(); }
})();

function savePlPinned() {
  localStorage.setItem('pl-pinned', JSON.stringify([...plPinned]));
}

function plDuration(secs) {
  if (!secs) return '—';
  const m = Math.floor(secs / 60), s = secs % 60;
  return m ? `${m}m ${s}s` : `${s}s`;
}

// Сортировка проектов по дате последнего пайплайна (новые наверх).
function sortProjectsByRecent(projects) {
  return [...projects].sort((a, b) => {
    const latest = arr => Math.max(0, ...(arr || []).map(p => new Date(p.updatedAt || p.createdAt || 0)));
    return latest(b.pipelines) - latest(a.pipelines);
  });
}

function renderPlFilterBar(projects) {
  const bar = document.getElementById('plFilterBar');
  if (!projects.length) { bar.innerHTML = ''; return; }

  const hasFilter = plPinned.size > 0;
  bar.innerHTML =
    `<span class="pl-filter-label">Filter</span>` +
    projects.map(p => {
      const active = plPinned.has(p.fullPath);
      return `<button class="pl-chip${active ? ' active' : ''}" data-path="${escapeHtml(p.fullPath)}">${escapeHtml(p.name)}</button>`;
    }).join('') +
    (hasFilter ? `<button class="pl-chip-clear" title="show all">✕ clear</button>` : '');

  bar.querySelectorAll('.pl-chip').forEach(chip => {
    chip.addEventListener('click', () => {
      const path = chip.dataset.path;
      if (plPinned.has(path)) plPinned.delete(path);
      else plPinned.add(path);
      savePlPinned();
      renderPlFilterBar(projects);
      renderPlList(projects);
    });
  });
  bar.querySelector('.pl-chip-clear')?.addEventListener('click', () => {
    plPinned.clear();
    savePlPinned();
    renderPlFilterBar(projects);
    renderPlList(projects);
  });
}

function renderPlList(projects) {
  const listEl = document.getElementById('pipelinesList');
  const visible = plPinned.size > 0 ? projects.filter(p => plPinned.has(p.fullPath)) : projects;

  listEl.innerHTML = visible.map(proj => `
    <div class="pl-project">
      <div class="pl-project-head">
        <a href="${escapeHtml(proj.webURL || '#')}" target="_blank" class="pl-project-name">${escapeHtml(proj.name)}</a>
        <span class="pl-project-path">${escapeHtml(proj.fullPath)}</span>
        <button class="pl-trends-btn" data-pid="${proj.id}" title="pipeline success trend · last 24h">Trends ▸</button>
      </div>
      <div class="pl-trend-panel" id="pl-trend-${proj.id}"></div>
      ${(proj.pipelines || []).map(pl => {
        const sc = PL_STATUS_CLASS[pl.status] || 'offline';
        const canRetry  = PL_CAN_RETRY.has(pl.status);
        const canCancel = PL_CAN_CANCEL.has(pl.status);
        const retryBtn  = canRetry  ? `<button class="pl-btn pl-retry"  data-pid="${proj.id}" data-plid="${pl.id}" data-path="${escapeHtml(proj.fullPath)}" title="retry">↺ restart</button>` : '';
        const cancelBtn = canCancel ? `<button class="pl-btn pl-cancel" data-pid="${proj.id}" data-plid="${pl.id}" data-path="${escapeHtml(proj.fullPath)}" title="cancel">✕</button>` : '';
        const meta = [
          pl.user ? `by <span class="pl-user">${escapeHtml(pl.user)}</span>` : '',
          pl.duration ? `<span class="pl-dur">${plDuration(pl.duration)}</span>` : '',
          `<span class="pl-ago">${relTime(pl.updatedAt || pl.createdAt)}</span>`,
        ].filter(Boolean).join(' · ');
        const dotTip = [
          pl.status,
          pl.duration ? plDuration(pl.duration) : null,
          relTime(pl.updatedAt || pl.createdAt),
        ].filter(Boolean).join(' · ');
        return `
        <div class="pl-row">
          <span class="pl-dot ${sc}" title="${escapeHtml(dotTip)}"></span>
          <div class="pl-info">
            <div class="pl-line1">
              <a href="${escapeHtml(pl.webURL || '#')}" target="_blank" class="pl-id">#${pl.id}</a>
              <span class="pl-sep">/</span>
              <span class="pl-branch-name">${escapeHtml(pl.ref || '—')}</span>
            </div>
            <div class="pl-line2">${meta}</div>
          </div>
          <div class="pl-actions">${retryBtn}${cancelBtn}</div>
        </div>`;
      }).join('')}
    </div>`).join('');

  listEl.querySelectorAll('.pl-btn').forEach(btn => {
    btn.addEventListener('click', async () => {
      const action = btn.classList.contains('pl-retry') ? 'retry' : 'cancel';
      btn.disabled = true;
      btn.textContent = '…';
      await pipelineAction(action,
        parseInt(btn.dataset.pid), parseInt(btn.dataset.plid), btn.dataset.path);
      await refreshPipelines();
    });
  });

}

function renderPipelines(projects) {
  const countEl = document.getElementById('pipelinesCount');
  const listEl  = document.getElementById('pipelinesList');
  if (!projects || !projects.length) {
    countEl.textContent = '0 projects';
    document.getElementById('plFilterBar').innerHTML = '';
    listEl.innerHTML = '<div class="empty">no pipelines — configure GitLab with -gitlab-url and -gitlab-token</div>';
    return;
  }
  const sorted = sortProjectsByRecent(projects);
  const total  = sorted.reduce((n, p) => n + (p.pipelines || []).length, 0);
  const visible = plPinned.size > 0 ? sorted.filter(p => plPinned.has(p.fullPath)) : sorted;
  countEl.textContent = `${visible.length}/${sorted.length} projects · ${total} pipelines`;

  renderPlFilterBar(sorted);
  renderPlList(sorted);
}

async function pipelineAction(action, projectId, pipelineId, fullPath) {
  try {
    const resp = await fetch(API_BASE + '/api/pipelines/action', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action, projectId, pipelineId, fullPath }),
    });
    if (!resp.ok) console.error('pipeline action failed:', resp.status);
  } catch (e) {
    console.error('pipeline action error:', e);
  }
}

// ================== TRENDS ==================

const TREND_COLOR = {
  success: 'var(--accent)', failed: 'var(--err)', running: 'var(--info)',
  pending: 'var(--warn)', canceled: 'var(--text-3)', skipped: 'var(--text-3)',
};

async function toggleTrend(btn, projectId) {
  const panel = document.getElementById(`pl-trend-${projectId}`);
  if (!panel) return;
  const open = panel.classList.contains('open');
  if (open) {
    panel.classList.remove('open');
    btn.textContent = 'Trends ▸';
    return;
  }
  panel.classList.add('open');
  btn.textContent = 'Trends ▾';
  panel.innerHTML = '<span class="pl-trend-loading">loading…</span>';
  try {
    const resp = await fetch(`${API_BASE}/api/pipelines/trends?project=${projectId}`, {
      headers: { 'Accept': 'application/json' },
    });
    renderTrendPanel(panel, resp.ok ? await resp.json() : []);
  } catch (e) {
    console.error('trends fetch error:', e);
    renderTrendPanel(panel, []);
  }
}

function renderTrendPanel(panel, items) {
  if (!items || !items.length) {
    panel.innerHTML = '<span class="pl-trend-loading">no data for last 24h</span>';
    return;
  }
  const total   = items.length;
  const success = items.filter(p => p.status === 'success').length;
  const failed  = items.filter(p => p.status === 'failed').length;
  const pct     = Math.round(success / total * 100);

  const blocks = items.map(p => {
    const color = TREND_COLOR[p.status] || 'var(--text-3)';
    const tip   = `#${p.id} · ${p.status} · ${escapeHtml(p.ref)} · ${relTime(p.createdAt)}`;
    return `<a href="${escapeHtml(p.webURL || '#')}" target="_blank"
               class="pl-trend-block" style="background:${color}" title="${tip}"></a>`;
  }).join('');

  panel.innerHTML = `
    <div class="pl-trend-stats">
      <span>${total} runs · <strong>${pct}%</strong> success</span>
      <span class="pl-trend-legend">
        <span class="pl-tl-dot" style="background:var(--accent)"></span>${success} ok
        <span class="pl-tl-dot" style="background:var(--err)"></span>${failed} failed
      </span>
    </div>
    <div class="pl-trend-bar">${blocks}</div>`;
}

// ================== TOOLS ==================

function renderTools(tools) {
  const grid = document.getElementById('toolsGrid');
  grid.innerHTML = tools.map(t => {
    const ready = !!t.url;
    const foot = ready
      ? `<span class="tool-status ready">configured</span>
         <a href="${escapeHtml(t.url)}" target="_blank" class="tool-open">OPEN ↗</a>`
      : `<span class="tool-status">tbd</span>
         <span class="tool-disabled">OPEN ↗</span>`;
    return `
    <div class="tool-card">
      <div class="tool-head">
        <span class="tool-name">${escapeHtml(t.name)}</span>
        <span class="tool-tag ${escapeHtml(t.tag)}">${escapeHtml(t.tag)}</span>
      </div>
      <div class="tool-desc">${escapeHtml(t.desc)}</div>
      <div class="tool-foot">${foot}</div>
    </div>`;
  }).join('');
}

function renderAbout(a) {
  document.getElementById('aboutUptime').textContent = 'up ' + a.uptime;

  const cfgRows = Object.entries(a.config).map(([k, v]) => `
    <div class="about-kv">
      <span class="about-key">${escapeHtml(k)}</span>
      <span class="about-val">${escapeHtml(v)}</span>
    </div>`).join('');

  const groups = [];
  const groupMap = {};
  for (const e of a.endpoints) {
    if (!groupMap[e.group]) { groupMap[e.group] = []; groups.push(e.group); }
    groupMap[e.group].push(e);
  }
  const epRows = groups.map(g => `
    <div class="about-group-title">${escapeHtml(g)}</div>
    ${groupMap[g].map(e => `
    <div class="about-ep">
      <span class="about-method ${e.method === 'POST' ? 'post' : 'get'}">${e.method}</span>
      <span class="about-path">${escapeHtml(e.path)}</span>
      <span class="about-desc">${escapeHtml(e.desc)}</span>
    </div>`).join('')}`).join('');

  document.getElementById('aboutBody').innerHTML = `
    <div class="about-cols">
      <div>
        <div class="about-section-title">Config</div>
        ${cfgRows}
      </div>
      <div>
        <div class="about-section-title">API Endpoints</div>
        ${epRows}
      </div>
    </div>`;
}

async function refreshTools() {
  try {
    const [tools, about] = await Promise.all([api('/api/tools'), api('/api/about')]);
    renderTools(tools);
    renderAbout(about);
  } catch (e) {
    console.error('tools', e);
  }
}

async function refreshPipelines() {
  try {
    const resp = await fetch(API_BASE + '/api/pipelines', { headers: { 'Accept': 'application/json' } });
    if (!resp.ok) {
      document.getElementById('pipelinesList').innerHTML =
        `<div class="empty err">pipelines API error ${resp.status} — check server log</div>`;
      return;
    }
    renderPipelines(await resp.json());
  } catch (e) {
    console.error(e);
    document.getElementById('pipelinesList').innerHTML =
      '<div class="empty err">cannot reach backend</div>';
  }
}

// ================== REFRESH INTERVAL ==================

const REFRESH_PRESETS = [10, 30, 60, 0];
let refreshTimer = null;

function getRefreshInterval() {
  const fromUrl = parseInt(params.get('refresh'));
  if (REFRESH_PRESETS.includes(fromUrl)) return fromUrl;
  const fromStorage = parseInt(localStorage.getItem('refresh-interval'));
  if (REFRESH_PRESETS.includes(fromStorage)) return fromStorage;
  return 30;
}

function applyRefreshInterval(sec) {
  localStorage.setItem('refresh-interval', sec);
  const u = new URL(location);
  if (sec === 30) u.searchParams.delete('refresh');
  else u.searchParams.set('refresh', sec);
  history.replaceState(null, '', u);

  clearInterval(refreshTimer);
  if (sec === 0) return;

  refreshTimer = setInterval(() => {
    refresh();
    refreshClusterStatus();
    refreshServers();
    refreshRunners();
    refreshPipelines();
    if (CURRENT_VIEW === 'serverDetail' && CURRENT_DETAIL_SERVER) {
      loadServerDetail(CURRENT_DETAIL_SERVER);
    }
  }, sec * 1000);
}

function initRefreshSelect() {
  const btns = document.querySelectorAll('.refresh-btn');
  const cur = getRefreshInterval();
  const activate = val => btns.forEach(b => b.classList.toggle('active', parseInt(b.dataset.val) === val));
  activate(cur);
  applyRefreshInterval(cur);
  btns.forEach(btn => btn.addEventListener('click', () => {
    const val = parseInt(btn.dataset.val);
    activate(val);
    applyRefreshInterval(val);
  }));
}

(async () => {
  await loadClusters();
  initRunnersCollapse();

  // Делегированный listener — переживает любой перерендер пайплайнов.
  document.getElementById('pipelinesList').addEventListener('click', e => {
    const btn = e.target.closest('.pl-trends-btn');
    if (btn) toggleTrend(btn, parseInt(btn.dataset.pid));
  });

  refresh();
  refreshServers();
  refreshRunners();
  refreshPipelines();
  applyHashRoute();
  if (!location.hash.startsWith('#/server/')) {
    const saved = localStorage.getItem('current-view');
    if (saved && saved !== 'cluster') switchView(saved);
  }
  initRefreshSelect();
})();
