const setupCard = document.querySelector('#setupCard');
const loginCard = document.querySelector('#loginCard');
const dashboard = document.querySelector('#dashboard');
const statusChip = document.querySelector('#statusChip');
const setupError = document.querySelector('#setupError');
const loginError = document.querySelector('#loginError');
const storageLabel = document.querySelector('#storageLabel');
const mediaGrid = document.querySelector('#mediaGrid');
const previewPane = document.querySelector('#previewPane');
const auditTrail = document.querySelector('#auditTrail');

const setupForm = document.querySelector('#setupForm');
const loginForm = document.querySelector('#loginForm');
const refreshBtn = document.querySelector('#refreshBtn');
const logoutBtn = document.querySelector('#logoutBtn');

let map;
let mapLayer;

init().catch((err) => {
  console.error(err);
  statusChip.textContent = 'Initialization error';
});

async function init() {
  bindEvents();
  await refreshAuthState();
}

function bindEvents() {
  setupForm?.addEventListener('submit', async (event) => {
    event.preventDefault();
    setupError.textContent = '';
    const data = Object.fromEntries(new FormData(setupForm).entries());
    try {
      await api('/api/setup', { method: 'POST', body: data });
      await refreshAuthState();
    } catch (err) {
      setupError.textContent = err.message;
    }
  });

  loginForm?.addEventListener('submit', async (event) => {
    event.preventDefault();
    loginError.textContent = '';
    const data = Object.fromEntries(new FormData(loginForm).entries());
    try {
      await api('/api/login', { method: 'POST', body: data });
      await refreshAuthState();
    } catch (err) {
      loginError.textContent = err.message;
    }
  });

  refreshBtn?.addEventListener('click', async () => {
    await loadDashboardData();
  });

  logoutBtn?.addEventListener('click', async () => {
    await api('/api/logout', { method: 'POST', body: {} });
    await refreshAuthState();
  });
}

async function refreshAuthState() {
  const status = await api('/api/status');

  setupCard.classList.add('hidden');
  loginCard.classList.add('hidden');
  dashboard.classList.add('hidden');

  if (!status.has_users) {
    statusChip.textContent = 'Setup required';
    setupCard.classList.remove('hidden');
    return;
  }

  if (!status.authenticated) {
    statusChip.textContent = 'Login required';
    loginCard.classList.remove('hidden');
    return;
  }

  statusChip.textContent = 'Authenticated';
  storageLabel.textContent = `Storage: ${status.storage_dir || 'Not configured'}`;
  dashboard.classList.remove('hidden');
  await loadDashboardData();
}

async function loadDashboardData() {
  const [mediaRes, mapRes, auditRes] = await Promise.all([
    api('/api/media?size=180&sort=capture_time&order=desc'),
    api('/api/map'),
    api('/api/audit')
  ]);

  renderMedia(mediaRes.items || []);
  renderMap(mapRes.points || []);
  renderAudit(auditRes.items || []);
}

function renderMedia(items) {
  mediaGrid.innerHTML = '';
  if (!items.length) {
    mediaGrid.innerHTML = '<p>No media imported yet. Insert a USB drive to trigger ingestion.</p>';
    return;
  }

  items.forEach((item) => {
    const tile = document.createElement('article');
    tile.className = 'tile';

    const mediaEl = item.kind === 'video'
      ? `<video muted preload="metadata" src="${item.preview_url}"></video>`
      : `<img loading="lazy" src="${item.preview_url}" alt="${escapeHtml(item.file_name)}"/>`;

    tile.innerHTML = `${mediaEl}
      <div class="meta">
        <div title="${escapeHtml(item.file_name)}">${escapeHtml(item.file_name)}</div>
        <div>${new Date(item.capture_time).toLocaleString()}</div>
      </div>`;

    tile.addEventListener('click', () => renderPreview(item));
    mediaGrid.appendChild(tile);
  });
}

function renderPreview(item) {
  const safeName = escapeHtml(item.file_name);
  const ts = new Date(item.capture_time).toLocaleString();

  previewPane.className = 'preview-pane';
  if (item.kind === 'video') {
    previewPane.innerHTML = `
      <video controls autoplay src="${item.preview_url}"></video>
      <p><strong>${safeName}</strong><br/>${ts}</p>
    `;
    return;
  }

  previewPane.innerHTML = `
    <img src="${item.preview_url}" alt="${safeName}" />
    <p><strong>${safeName}</strong><br/>${ts}</p>
  `;
}

function renderMap(points) {
  if (!map) {
    map = L.map('map', { zoomControl: true }).setView([20, 0], 2);
    L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
      maxZoom: 19,
      attribution: '&copy; OpenStreetMap contributors'
    }).addTo(map);
  }

  if (mapLayer) {
    map.removeLayer(mapLayer);
  }

  mapLayer = L.layerGroup();
  const bounds = [];

  points.forEach((p) => {
    if (typeof p.lat !== 'number' || typeof p.lon !== 'number') return;
    const marker = L.marker([p.lat, p.lon]).bindPopup(`${escapeHtml(p.file_name)}<br/>${new Date(p.capture_time).toLocaleString()}`);
    marker.addTo(mapLayer);
    bounds.push([p.lat, p.lon]);
  });

  mapLayer.addTo(map);
  if (bounds.length) {
    map.fitBounds(bounds, { padding: [20, 20] });
  }
}

function renderAudit(items) {
  auditTrail.innerHTML = '';
  const slice = items.slice(0, 40);
  if (!slice.length) {
    auditTrail.innerHTML = '<div class="audit-line">No audit events yet.</div>';
    return;
  }

  slice.forEach((item) => {
    const row = document.createElement('div');
    row.className = 'audit-line';
    row.textContent = `${new Date(item.ts).toLocaleString()} | ${item.actor} | ${item.action}`;
    auditTrail.appendChild(row);
  });
}

async function api(url, options = {}) {
  const init = {
    method: options.method || 'GET',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' }
  };

  if (options.body !== undefined) {
    init.body = JSON.stringify(options.body);
  }

  const response = await fetch(url, init);
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(payload.error || `Request failed: ${response.status}`);
  }
  return payload;
}

function escapeHtml(value) {
  return String(value)
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}
