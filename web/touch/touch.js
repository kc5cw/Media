const statusEl = document.querySelector('#status');
const storageEl = document.querySelector('#storage');
const tilesEl = document.querySelector('#tiles');
const viewerInner = document.querySelector('#viewerInner');
const refreshBtn = document.querySelector('#refresh');
const logoutBtn = document.querySelector('#logout');

let map;
let mapLayer;

init().catch((err) => {
  console.error(err);
  statusEl.textContent = 'Error';
});

async function init() {
  refreshBtn.addEventListener('click', () => loadAll());
  logoutBtn.addEventListener('click', async () => {
    await api('/api/logout', { method: 'POST', body: {} });
    window.location.href = '/';
  });

  await ensureAuthed();
  await loadAll();
}

async function ensureAuthed() {
  const st = await api('/api/status');
  if (!st.has_users) {
    statusEl.textContent = 'Setup required on main UI';
    window.location.href = '/';
    return;
  }
  if (!st.authenticated) {
    statusEl.textContent = 'Login required on main UI';
    window.location.href = '/';
    return;
  }
  statusEl.textContent = 'Ready';
  storageEl.textContent = st.storage_dir ? `Storage: ${st.storage_dir}` : 'Storage not configured';
}

async function loadAll() {
  statusEl.textContent = 'Loading...';

  const [mediaRes, mapRes] = await Promise.all([
    api('/api/media?size=220&sort=capture_time&order=desc'),
    api('/api/map')
  ]);

  renderMedia(mediaRes.items || []);
  renderMap(mapRes.points || []);

  statusEl.textContent = 'Ready';
}

function renderMedia(items) {
  tilesEl.innerHTML = '';
  if (!items.length) {
    tilesEl.innerHTML = '<div class="sub">No media imported yet. Insert a USB drive to trigger ingestion.</div>';
    return;
  }

  for (const item of items) {
    const tile = document.createElement('div');
    tile.className = 'tile';

    const preview = item.kind === 'video'
      ? `<video muted preload="metadata" src="${item.preview_url}"></video>`
      : `<img loading="lazy" src="${item.preview_url}" alt="${escapeHtml(item.file_name)}"/>`;

    tile.innerHTML = `${preview}
      <div class="meta">
        <div class="name" title="${escapeHtml(item.file_name)}">${escapeHtml(item.file_name)}</div>
        <div class="time">${new Date(item.capture_time).toLocaleString()}</div>
      </div>`;

    tile.addEventListener('click', () => renderViewer(item));
    tilesEl.appendChild(tile);
  }
}

function renderViewer(item) {
  viewerInner.innerHTML = '';

  if (item.kind === 'video') {
    const v = document.createElement('video');
    v.controls = true;
    v.autoplay = true;
    v.src = item.preview_url;
    viewerInner.appendChild(v);
    return;
  }

  const img = document.createElement('img');
  img.src = item.preview_url;
  img.alt = item.file_name;
  viewerInner.appendChild(img);
}

function renderMap(points) {
  if (!map) {
    map = L.map('map', { zoomControl: false }).setView([20, 0], 2);
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

  for (const p of points) {
    if (typeof p.lat !== 'number' || typeof p.lon !== 'number') continue;
    const marker = L.circleMarker([p.lat, p.lon], {
      radius: 8,
      color: '#2be7ff',
      weight: 2,
      fillColor: '#44ffb0',
      fillOpacity: 0.25
    }).bindPopup(`${escapeHtml(p.file_name)}<br/>${new Date(p.capture_time).toLocaleString()}`);
    marker.addTo(mapLayer);
    bounds.push([p.lat, p.lon]);
  }

  mapLayer.addTo(map);
  if (bounds.length) {
    map.fitBounds(bounds, { padding: [10, 10] });
  }
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
