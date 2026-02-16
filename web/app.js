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
const activeFilter = document.querySelector('#activeFilter');

const ingestWidget = document.querySelector('#ingestWidget');
const ingestRing = document.querySelector('#ingestRing');
const ingestRingLabel = document.querySelector('#ingestRingLabel');
const ingestTitle = document.querySelector('#ingestTitle');
const ingestSub = document.querySelector('#ingestSub');

const placesState = document.querySelector('#placesState');
const placesCounty = document.querySelector('#placesCounty');
const placesCity = document.querySelector('#placesCity');
const placesRoad = document.querySelector('#placesRoad');
const placeCrumb = document.querySelector('#placeCrumb');
const clearPlaceBtn = document.querySelector('#clearPlaceBtn');

const mapExpandBtn = document.querySelector('#mapExpandBtn');
const mapModal = document.querySelector('#mapModal');
const mapCloseBtn = document.querySelector('#mapCloseBtn');

const setupForm = document.querySelector('#setupForm');
const loginForm = document.querySelector('#loginForm');
const refreshBtn = document.querySelector('#refreshBtn');
const logoutBtn = document.querySelector('#logoutBtn');

let map;
let mapLayer;
let mapFull;
let mapFullLayer;
let lastPoints = [];
let ingestPoller;

function leaflet() {
  // Leaflet's UMD build sets window.L (and window.leaflet). In ES modules, bare `L`
  // may not be defined, so always resolve via window.
  return window.L || window.leaflet;
}

const locFilter = {
  state: '',
  county: '',
  city: '',
  road: ''
};

init().catch((err) => {
  console.error(err);
  statusChip.textContent = `Initialization error: ${err?.message || err}`;
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

  clearPlaceBtn?.addEventListener('click', async () => {
    locFilter.state = '';
    locFilter.county = '';
    locFilter.city = '';
    locFilter.road = '';
    await loadDashboardData();
  });

  mapExpandBtn?.addEventListener('click', () => {
    openMapModal();
  });
  mapCloseBtn?.addEventListener('click', () => {
    closeMapModal();
  });
  mapModal?.addEventListener('click', (e) => {
    if (e.target === mapModal) closeMapModal();
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && mapModal && !mapModal.classList.contains('hidden')) {
      closeMapModal();
    }
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
    stopIngestPolling();
    return;
  }

  if (!status.authenticated) {
    statusChip.textContent = 'Login required';
    loginCard.classList.remove('hidden');
    stopIngestPolling();
    return;
  }

  statusChip.textContent = 'Authenticated';
  storageLabel.textContent = `Storage: ${status.storage_dir || 'Not configured'}`;
  dashboard.classList.remove('hidden');
  startIngestPolling();
  await loadDashboardData();
}

async function loadDashboardData() {
  await loadPlaces();
  const [mediaRes, mapRes, auditRes] = await Promise.all([
    api(`/api/media?size=180&sort=capture_time&order=desc${filterQuery('&')}`),
    api(`/api/map${filterQuery('?')}`),
    api('/api/audit')
  ]);

  renderFilterChip();
  renderMedia(mediaRes.items || []);
  renderMap(mapRes.points || []);
  renderAudit(auditRes.items || []);
}

async function loadPlaces() {
  const stateRes = await api('/api/location-groups?level=state');
  renderPlaceList(placesState, stateRes.groups || [], 'state');

  if (locFilter.state) {
    const countyRes = await api(`/api/location-groups?level=county&state=${encodeURIComponent(locFilter.state)}`);
    renderPlaceList(placesCounty, countyRes.groups || [], 'county');
  } else {
    placesCounty.innerHTML = '<div class="muted">Select a state</div>';
  }

  if (locFilter.state && locFilter.county) {
    const cityRes = await api(`/api/location-groups?level=city&state=${encodeURIComponent(locFilter.state)}&county=${encodeURIComponent(locFilter.county)}`);
    renderPlaceList(placesCity, cityRes.groups || [], 'city');
  } else {
    placesCity.innerHTML = '<div class="muted">Select a county</div>';
  }

  if (locFilter.state && locFilter.county && locFilter.city) {
    const roadRes = await api(`/api/location-groups?level=road&state=${encodeURIComponent(locFilter.state)}&county=${encodeURIComponent(locFilter.county)}&city=${encodeURIComponent(locFilter.city)}`);
    renderPlaceList(placesRoad, roadRes.groups || [], 'road');
  } else {
    placesRoad.innerHTML = '<div class="muted">Select a city</div>';
  }

  renderCrumb();
}

function renderPlaceList(container, groups, level) {
  container.innerHTML = '';
  const slice = groups.slice(0, 100);
  if (!slice.length) {
    container.innerHTML = '<div class="muted">No data</div>';
    return;
  }

  slice.forEach((g) => {
    const row = document.createElement('div');
    row.className = 'place-item';
    row.innerHTML = `<div class="name" title="${escapeHtml(g.name)}">${escapeHtml(g.name)}</div><div class="count">${g.count}</div>`;
    row.addEventListener('click', async () => {
      if (level === 'state') {
        locFilter.state = g.name;
        locFilter.county = '';
        locFilter.city = '';
        locFilter.road = '';
      } else if (level === 'county') {
        locFilter.county = g.name;
        locFilter.city = '';
        locFilter.road = '';
      } else if (level === 'city') {
        locFilter.city = g.name;
        locFilter.road = '';
      } else if (level === 'road') {
        locFilter.road = g.name;
      }
      await loadDashboardData();
    });
    container.appendChild(row);
  });
}

function renderCrumb() {
  const parts = [];
  if (locFilter.state) parts.push(locFilter.state);
  if (locFilter.county) parts.push(locFilter.county);
  if (locFilter.city) parts.push(locFilter.city);
  if (locFilter.road) parts.push(locFilter.road);
  placeCrumb.textContent = parts.length ? `Selected: ${parts.join(' / ')}` : 'Selected: (none)';
}

function renderFilterChip() {
  if (!activeFilter) return;
  const parts = [];
  if (locFilter.state) parts.push(`State: ${locFilter.state}`);
  if (locFilter.county) parts.push(`County: ${locFilter.county}`);
  if (locFilter.city) parts.push(`City: ${locFilter.city}`);
  if (locFilter.road) parts.push(`Street: ${locFilter.road}`);
  activeFilter.textContent = parts.length ? parts.join(' | ') : 'All media';
}

function filterQuery(prefix) {
  const params = new URLSearchParams();
  if (locFilter.state) params.set('state', locFilter.state);
  if (locFilter.county) params.set('county', locFilter.county);
  if (locFilter.city) params.set('city', locFilter.city);
  if (locFilter.road) params.set('road', locFilter.road);
  const q = params.toString();
  return q ? `${prefix}${q}` : '';
}

function renderMedia(items) {
  mediaGrid.innerHTML = '';
  if (!items.length) {
    mediaGrid.innerHTML = '<p>No media found for this filter.</p>';
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
        <div class="muted">${escapeHtml(item.location || '')}</div>
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
  lastPoints = points || [];
  const L = leaflet();
  if (!L) {
    const el = document.getElementById('map');
    if (el) el.innerHTML = '<div class="muted">Map unavailable (Leaflet failed to load).</div>';
    return;
  }
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

function openMapModal() {
  if (!mapModal) return;
  mapModal.classList.remove('hidden');
  mapModal.setAttribute('aria-hidden', 'false');

  const L = leaflet();
  if (!L) {
    const el = document.getElementById('mapFull');
    if (el) el.innerHTML = '<div class="muted">Map unavailable (Leaflet failed to load).</div>';
    return;
  }
  if (!mapFull) {
    mapFull = L.map('mapFull', { zoomControl: true }).setView([20, 0], 2);
    L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
      maxZoom: 19,
      attribution: '&copy; OpenStreetMap contributors'
    }).addTo(mapFull);
  }

  renderMapFull(lastPoints);
  setTimeout(() => mapFull.invalidateSize(), 50);
}

function closeMapModal() {
  if (!mapModal) return;
  mapModal.classList.add('hidden');
  mapModal.setAttribute('aria-hidden', 'true');
}

function renderMapFull(points) {
  const L = leaflet();
  if (!L) return;
  if (!mapFull) return;
  if (mapFullLayer) mapFull.removeLayer(mapFullLayer);
  mapFullLayer = L.layerGroup();
  const bounds = [];
  (points || []).forEach((p) => {
    if (typeof p.lat !== 'number' || typeof p.lon !== 'number') return;
    const marker = L.marker([p.lat, p.lon]).bindPopup(`${escapeHtml(p.file_name)}<br/>${new Date(p.capture_time).toLocaleString()}`);
    marker.addTo(mapFullLayer);
    bounds.push([p.lat, p.lon]);
  });
  mapFullLayer.addTo(mapFull);
  if (bounds.length) {
    mapFull.fitBounds(bounds, { padding: [20, 20] });
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

function startIngestPolling() {
  if (ingestPoller) return;
  ingestPoller = setInterval(() => {
    refreshIngestStatus().catch(() => {});
  }, 1000);
  refreshIngestStatus().catch(() => {});
}

function stopIngestPolling() {
  if (!ingestPoller) return;
  clearInterval(ingestPoller);
  ingestPoller = undefined;
}

async function refreshIngestStatus() {
  if (!ingestWidget) return;
  const st = await api('/api/ingest-status');
  renderIngestStatus(st);
}

function renderIngestStatus(st) {
  const state = (st.state || 'idle').toLowerCase();
  const phase = st.phase || '';

  let title = 'Idle';
  let label = 'Idle';
  let sub = 'Waiting for USB...';
  let p = 0;

  if (state === 'scanning') {
    title = 'Scanning';
    label = '...';
    sub = `${st.total_files || 0} files found`;
  } else if (state === 'ingesting') {
    title = 'Ingesting';
    p = Number(st.percent || 0);
    const total = st.total_files || 0;
    const done = st.processed_files || 0;
    label = total > 0 ? `${Math.floor(p)}%` : '...';

    const fps = Number(st.files_per_sec || 0);
    const mbps = Number(st.mbps || 0);
    sub = `${done}/${total} files | ${fps.toFixed(1)} files/s | ${mbps.toFixed(1)} MB/s`;
  } else if (state === 'error') {
    title = 'Error';
    label = '!';
    sub = st.message || 'Ingest error';
  } else {
    if (st.last_result && (st.last_result.copied || st.last_result.duplicates || st.last_result.errors)) {
      title = 'Idle';
      label = 'Idle';
      sub = `Last: copied ${st.last_result.copied}, dup ${st.last_result.duplicates}, err ${st.last_result.errors}`;
    }
  }

  if (ingestRing) ingestRing.style.setProperty('--p', Math.max(0, Math.min(100, p)));
  if (ingestRingLabel) ingestRingLabel.textContent = label;
  if (ingestTitle) ingestTitle.textContent = title;
  if (ingestSub) ingestSub.textContent = sub;

  // Tooltip with current file path if available.
  if (ingestWidget) ingestWidget.title = st.current_path || '';
}
