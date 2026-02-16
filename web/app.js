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
const reloadMountPolicyBtn = document.querySelector('#reloadMountPolicyBtn');
const mountPolicyList = document.querySelector('#mountPolicyList');
const mountPolicyMsg = document.querySelector('#mountPolicyMsg');
const selectionInfo = document.querySelector('#selectionInfo');
const selectAllBtn = document.querySelector('#selectAllBtn');
const clearSelectionBtn = document.querySelector('#clearSelectionBtn');
const deleteSelectedBtn = document.querySelector('#deleteSelectedBtn');
const downloadSelectedFilesBtn = document.querySelector('#downloadSelectedFilesBtn');
const downloadSelectedZipBtn = document.querySelector('#downloadSelectedZipBtn');
const deleteCurrentBtn = document.querySelector('#deleteCurrentBtn');
const downloadCurrentBtn = document.querySelector('#downloadCurrentBtn');
const backupForm = document.querySelector('#backupForm');
const backupMode = document.querySelector('#backupMode');
const backupDestination = document.querySelector('#backupDestination');
const backupSSHPort = document.querySelector('#backupSSHPort');
const backupAPIMethod = document.querySelector('#backupAPIMethod');
const backupAPIToken = document.querySelector('#backupAPIToken');
const backupStatus = document.querySelector('#backupStatus');
const textFilterInput = document.querySelector('#textFilterInput');
const kindFilterSelect = document.querySelector('#kindFilterSelect');
const gpsFilterSelect = document.querySelector('#gpsFilterSelect');
const captureFromInput = document.querySelector('#captureFromInput');
const captureToInput = document.querySelector('#captureToInput');
const applyMediaFilterBtn = document.querySelector('#applyMediaFilterBtn');
const resetMediaFilterBtn = document.querySelector('#resetMediaFilterBtn');
const sortBySelect = document.querySelector('#sortBySelect');
const sortOrderSelect = document.querySelector('#sortOrderSelect');
const nearLatInput = document.querySelector('#nearLatInput');
const nearLonInput = document.querySelector('#nearLonInput');
const viewAllMediaBtn = document.querySelector('#viewAllMediaBtn');
const viewAlbumsBtn = document.querySelector('#viewAlbumsBtn');
const albumActions = document.querySelector('#albumActions');
const albumViewPanel = document.querySelector('#albumViewPanel');
const albumList = document.querySelector('#albumList');
const autoAlbumStateList = document.querySelector('#autoAlbumStateList');
const newAlbumNameInput = document.querySelector('#newAlbumNameInput');
const createAlbumBtn = document.querySelector('#createAlbumBtn');
const addSelectedToAlbumBtn = document.querySelector('#addSelectedToAlbumBtn');
const removeSelectedFromAlbumBtn = document.querySelector('#removeSelectedFromAlbumBtn');

let map;
let mapLayer;
let mapFull;
let mapFullLayer;
let lastPoints = [];
let ingestPoller;
let mountPolicy = { mounts: [], excluded_mounts: [], auto_excluded_mounts: [] };
let mediaItems = [];
let selectedIDs = new Set();
let currentPreviewID = null;
let viewMode = 'all';
let albums = [];
let activeAlbumID = 0;

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

const mediaFilter = {
  q: '',
  kind: '',
  gps: '',
  from: '',
  to: '',
  sort: 'capture_time',
  order: 'desc',
  nearLat: '',
  nearLon: ''
};

init().catch((err) => {
  console.error(err);
  statusChip.textContent = `Initialization error: ${err?.message || err}`;
});

async function init() {
  bindEvents();
  writeMediaFilterControls();
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

  reloadMountPolicyBtn?.addEventListener('click', async () => {
    await loadMountPolicy();
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

  selectAllBtn?.addEventListener('click', () => {
    mediaItems.forEach((item) => selectedIDs.add(Number(item.id)));
    renderMedia(mediaItems);
  });

  clearSelectionBtn?.addEventListener('click', () => {
    selectedIDs.clear();
    renderMedia(mediaItems);
  });

  deleteSelectedBtn?.addEventListener('click', async () => {
    await deleteSelectedMedia(Array.from(selectedIDs));
  });

  downloadSelectedFilesBtn?.addEventListener('click', () => {
    downloadSelectedAsFiles(Array.from(selectedIDs));
  });

  downloadSelectedZipBtn?.addEventListener('click', async () => {
    await downloadSelectedAsZip(Array.from(selectedIDs));
  });

  deleteCurrentBtn?.addEventListener('click', async () => {
    if (!currentPreviewID) return;
    await deleteSelectedMedia([currentPreviewID]);
  });

  downloadCurrentBtn?.addEventListener('click', () => {
    if (!currentPreviewID) return;
    downloadSelectedAsFiles([currentPreviewID]);
  });

  applyMediaFilterBtn?.addEventListener('click', async () => {
    readMediaFilterControls();
    if (mediaFilter.sort === 'distance' && (!mediaFilter.nearLat || !mediaFilter.nearLon)) {
      statusChip.textContent = 'For region proximity sort, provide both Near lat and Near lon.';
      return;
    }
    await loadDashboardData();
  });

  resetMediaFilterBtn?.addEventListener('click', async () => {
    mediaFilter.q = '';
    mediaFilter.kind = '';
    mediaFilter.gps = '';
    mediaFilter.from = '';
    mediaFilter.to = '';
    mediaFilter.sort = 'capture_time';
    mediaFilter.order = 'desc';
    mediaFilter.nearLat = '';
    mediaFilter.nearLon = '';
    writeMediaFilterControls();
    await loadDashboardData();
  });

  textFilterInput?.addEventListener('keydown', async (event) => {
    if (event.key !== 'Enter') return;
    event.preventDefault();
    readMediaFilterControls();
    await loadDashboardData();
  });

  viewAllMediaBtn?.addEventListener('click', async () => {
    viewMode = 'all';
    activeAlbumID = 0;
    selectedIDs.clear();
    renderViewModeState();
    await loadDashboardData();
  });

  viewAlbumsBtn?.addEventListener('click', async () => {
    viewMode = 'albums';
    selectedIDs.clear();
    renderViewModeState();
    await loadAlbums();
    await loadDashboardData();
  });

  createAlbumBtn?.addEventListener('click', async () => {
    const name = String(newAlbumNameInput?.value || '').trim();
    if (!name) {
      statusChip.textContent = 'Album name is required.';
      return;
    }
    try {
      const payload = await api('/api/albums', { method: 'POST', body: { name } });
      newAlbumNameInput.value = '';
      await loadAlbums();
      if (payload?.item?.id) {
        activeAlbumID = Number(payload.item.id);
      }
      renderViewModeState();
      await loadDashboardData();
    } catch (err) {
      statusChip.textContent = `Create album failed: ${err.message}`;
    }
  });

  addSelectedToAlbumBtn?.addEventListener('click', async () => {
    if (!activeAlbumID) {
      statusChip.textContent = 'Select an album first.';
      return;
    }
    const ids = Array.from(selectedIDs);
    if (!ids.length) return;
    try {
      const res = await api(`/api/albums/${activeAlbumID}/add`, { method: 'POST', body: { ids } });
      await loadAlbums();
      statusChip.textContent = `Album updated: added ${res.added || 0}, skipped ${res.skipped || 0}`;
      if (viewMode === 'albums') {
        await loadDashboardData();
      }
    } catch (err) {
      statusChip.textContent = `Add to album failed: ${err.message}`;
    }
  });

  removeSelectedFromAlbumBtn?.addEventListener('click', async () => {
    if (!activeAlbumID) {
      statusChip.textContent = 'Select an album first.';
      return;
    }
    const ids = Array.from(selectedIDs);
    if (!ids.length) return;
    try {
      const res = await api(`/api/albums/${activeAlbumID}/remove`, { method: 'POST', body: { ids } });
      selectedIDs.clear();
      await loadAlbums();
      statusChip.textContent = `Album updated: removed ${res.removed || 0}, skipped ${res.skipped || 0}`;
      await loadDashboardData();
    } catch (err) {
      statusChip.textContent = `Remove from album failed: ${err.message}`;
    }
  });

  backupForm?.addEventListener('submit', async (event) => {
    event.preventDefault();
    if (!backupDestination?.value.trim()) {
      renderBackupStatus({ state: 'error', message: 'Destination is required.' });
      return;
    }
    const mode = backupMode?.value || 'ssh';
    const destination = backupDestination.value.trim();
    if (!window.confirm(`Start backup now?\n\nMode: ${mode}\nDestination: ${destination}`)) {
      return;
    }
    try {
      await api('/api/backup', {
        method: 'POST',
        body: {
          mode,
          destination,
          ssh_port: Number(backupSSHPort?.value || 0) || 0,
          api_method: backupAPIMethod?.value || 'PUT',
          api_token: backupAPIToken?.value || ''
        }
      });
      renderBackupStatus({ state: 'running', mode, destination, message: 'Backup started...' });
      await refreshBackupStatus();
    } catch (err) {
      renderBackupStatus({ state: 'error', message: err.message });
    }
  });

  backupMode?.addEventListener('change', () => {
    if (!backupDestination) return;
    const mode = backupMode.value;
    if (mode === 'ssh') {
      backupDestination.placeholder = 'user@host:/backups/usbvault.tar.gz';
    } else if (mode === 'rsync') {
      backupDestination.placeholder = 'user@host:/backups/usbvault or /local/backup/dir';
    } else if (mode === 's3') {
      backupDestination.placeholder = 's3://bucket/usbvault/backup.tar.gz';
    } else if (mode === 'api') {
      backupDestination.placeholder = 'https://api.example.com/upload';
    }
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
    viewMode = 'all';
    activeAlbumID = 0;
    albums = [];
    selectedIDs.clear();
    clearPreview();
    return;
  }

  if (!status.authenticated) {
    statusChip.textContent = 'Login required';
    loginCard.classList.remove('hidden');
    stopIngestPolling();
    viewMode = 'all';
    activeAlbumID = 0;
    albums = [];
    selectedIDs.clear();
    clearPreview();
    return;
  }

  statusChip.textContent = 'Authenticated';
  storageLabel.textContent = `Storage: ${status.storage_dir || 'Not configured'}`;
  dashboard.classList.remove('hidden');
  renderViewModeState();
  startIngestPolling();
  await loadAlbums();
  await loadDashboardData();
}

async function loadDashboardData() {
  renderViewModeState();
  await Promise.all([
    loadPlaces(),
    loadMountPolicy().catch((err) => {
      if (mountPolicyMsg) mountPolicyMsg.textContent = `Mount policy unavailable: ${err.message}`;
    })
  ]);
  if (viewMode === 'albums') {
    await loadAlbums();
    await loadAutoAlbumStates();
  }

  const mediaSort = mediaFilter.sort || 'capture_time';
  const mediaOrder = mediaFilter.order || 'desc';
  const showAlbumMedia = !(viewMode === 'albums' && activeAlbumID === 0);
  const [mediaRes, mapRes, auditRes] = await Promise.all([
    showAlbumMedia
      ? api(`/api/media?size=180&sort=${encodeURIComponent(mediaSort)}&order=${encodeURIComponent(mediaOrder)}${filterQuery('&')}`)
      : Promise.resolve({ items: [] }),
    showAlbumMedia ? api(`/api/map${filterQuery('?')}`) : Promise.resolve({ points: [] }),
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
  if (mediaFilter.kind) parts.push(`Type: ${mediaFilter.kind}`);
  if (mediaFilter.gps === 'yes') parts.push('GPS: tagged');
  if (mediaFilter.gps === 'no') parts.push('GPS: none');
  if (mediaFilter.from) parts.push(`From: ${isoToDateValue(mediaFilter.from)}`);
  if (mediaFilter.to) parts.push(`To: ${isoToDateValue(mediaFilter.to)}`);
  if (mediaFilter.q) parts.push(`Search: "${mediaFilter.q}"`);
  if (mediaFilter.sort) parts.push(`Sort: ${mediaFilter.sort} (${mediaFilter.order || 'desc'})`);
  if (mediaFilter.sort === 'distance' && mediaFilter.nearLat && mediaFilter.nearLon) {
    parts.push(`Near: ${mediaFilter.nearLat}, ${mediaFilter.nearLon}`);
  }
  if (viewMode === 'albums') {
    if (activeAlbumID > 0) {
      const album = albums.find((a) => Number(a.id) === Number(activeAlbumID));
      parts.push(`Album: ${album?.name || activeAlbumID}`);
    } else {
      parts.push('Album View');
    }
  }
  activeFilter.textContent = parts.length ? parts.join(' | ') : 'All media';
}

async function loadMountPolicy() {
  if (!mountPolicyList) return;
  mountPolicyMsg.textContent = 'Loading mount sources...';
  const payload = await api('/api/mount-policy');
  mountPolicy = payload || { mounts: [], excluded_mounts: [], auto_excluded_mounts: [] };
  renderMountPolicy();
}

function renderMountPolicy() {
  if (!mountPolicyList) return;
  mountPolicyList.innerHTML = '';

  const mounts = mountPolicy.mounts || [];
  const excluded = new Set(mountPolicy.excluded_mounts || []);
  const autoExcluded = new Set(mountPolicy.auto_excluded_mounts || []);

  if (!mounts.length) {
    mountPolicyMsg.textContent = 'No removable mounts detected.';
    mountPolicyList.innerHTML = '<div class="muted">Plug in a USB/SD device to see it here.</div>';
    return;
  }

  mountPolicyMsg.textContent = '';

  mounts.forEach((mount) => {
    const row = document.createElement('div');
    row.className = 'mount-row';

    const path = document.createElement('div');
    path.className = 'mount-path';
    path.textContent = mount;
    path.title = mount;

    const actions = document.createElement('div');
    actions.className = 'mount-actions';

    if (autoExcluded.has(mount)) {
      const pill = document.createElement('div');
      pill.className = 'pill auto';
      pill.textContent = 'Auto ignored (storage drive)';
      actions.appendChild(pill);
    } else if (excluded.has(mount)) {
      const pill = document.createElement('div');
      pill.className = 'pill user';
      pill.textContent = 'Excluded';
      actions.appendChild(pill);
    } else {
      const pill = document.createElement('div');
      pill.className = 'pill';
      pill.textContent = 'Included';
      actions.appendChild(pill);
    }

    if (!autoExcluded.has(mount)) {
      const btn = document.createElement('button');
      btn.className = 'ghost small';
      btn.textContent = excluded.has(mount) ? 'Include' : 'Exclude';
      btn.addEventListener('click', async () => {
        btn.disabled = true;
        try {
          const updated = new Set(mountPolicy.excluded_mounts || []);
          if (updated.has(mount)) {
            updated.delete(mount);
          } else {
            updated.add(mount);
          }
          await api('/api/excluded-mounts', {
            method: 'POST',
            body: { mounts: Array.from(updated) }
          });
          await loadMountPolicy();
          mountPolicyMsg.textContent = 'Excluded mount list updated.';
        } catch (err) {
          mountPolicyMsg.textContent = `Failed to update exclusions: ${err.message}`;
        } finally {
          btn.disabled = false;
        }
      });
      actions.appendChild(btn);
    }

    row.appendChild(path);
    row.appendChild(actions);
    mountPolicyList.appendChild(row);
  });
}

async function loadAlbums() {
  if (!albumList) return;
  try {
    const payload = await api('/api/albums');
    albums = payload.items || [];
    if (activeAlbumID > 0 && !albums.find((a) => Number(a.id) === Number(activeAlbumID))) {
      activeAlbumID = 0;
    }
    renderAlbums();
  } catch (err) {
    albumList.innerHTML = `<div class="muted">Album list unavailable: ${escapeHtml(err.message)}</div>`;
  }
}

function renderAlbums() {
  if (!albumList) return;
  albumList.innerHTML = '';
  if (!albums.length) {
    albumList.innerHTML = '<div class="muted">No albums yet. Create one above.</div>';
    return;
  }
  albums.forEach((album) => {
    const row = document.createElement('div');
    row.className = 'album-item';
    if (Number(activeAlbumID) === Number(album.id)) {
      row.classList.add('active');
    }
    row.innerHTML = `<div class="name">${escapeHtml(album.name)}</div><div class="count">${Number(album.item_count || 0)}</div>`;
    row.addEventListener('click', async () => {
      activeAlbumID = Number(album.id);
      selectedIDs.clear();
      renderAlbums();
      renderViewModeState();
      await loadDashboardData();
    });
    albumList.appendChild(row);
  });
}

async function loadAutoAlbumStates() {
  if (!autoAlbumStateList) return;
  try {
    const stateRes = await api('/api/location-groups?level=state');
    renderAutoAlbumStates(stateRes.groups || []);
  } catch (err) {
    autoAlbumStateList.innerHTML = `<div class="muted">Auto folders unavailable: ${escapeHtml(err.message)}</div>`;
  }
}

function renderAutoAlbumStates(groups) {
  if (!autoAlbumStateList) return;
  autoAlbumStateList.innerHTML = '';
  const slice = (groups || []).slice(0, 100);
  if (!slice.length) {
    autoAlbumStateList.innerHTML = '<div class="muted">No EXIF location folders yet.</div>';
    return;
  }
  slice.forEach((g) => {
    const row = document.createElement('div');
    row.className = 'album-item';
    const selected = String(locFilter.state || '').toLowerCase() === String(g.name || '').toLowerCase();
    if (selected) row.classList.add('active');
    row.innerHTML = `<div class="name">${escapeHtml(g.name || 'Unknown')}</div><div class="count">${Number(g.count || 0)}</div>`;
    row.addEventListener('click', async () => {
      locFilter.state = g.name || '';
      locFilter.county = '';
      locFilter.city = '';
      locFilter.road = '';
      activeAlbumID = 0;
      viewMode = 'all';
      selectedIDs.clear();
      renderViewModeState();
      await loadDashboardData();
    });
    autoAlbumStateList.appendChild(row);
  });
}

function renderViewModeState() {
  const albumsMode = viewMode === 'albums';
  viewAllMediaBtn?.classList.toggle('active', !albumsMode);
  viewAlbumsBtn?.classList.toggle('active', albumsMode);
  albumActions?.classList.toggle('hidden', !albumsMode);
  albumViewPanel?.classList.toggle('hidden', !albumsMode);
  removeSelectedFromAlbumBtn?.classList.toggle('hidden', !(albumsMode && activeAlbumID > 0));
}

function filterQuery(prefix) {
  readMediaFilterControls();
  const params = new URLSearchParams();
  if (locFilter.state) params.set('state', locFilter.state);
  if (locFilter.county) params.set('county', locFilter.county);
  if (locFilter.city) params.set('city', locFilter.city);
  if (locFilter.road) params.set('road', locFilter.road);
  if (viewMode === 'albums' && activeAlbumID > 0) params.set('album_id', String(activeAlbumID));
  if (mediaFilter.kind) params.set('kind', mediaFilter.kind);
  if (mediaFilter.gps) params.set('gps', mediaFilter.gps);
  if (mediaFilter.q) params.set('q', mediaFilter.q);
  if (mediaFilter.from) params.set('from', mediaFilter.from);
  if (mediaFilter.to) params.set('to', mediaFilter.to);
  if (mediaFilter.nearLat) params.set('near_lat', mediaFilter.nearLat);
  if (mediaFilter.nearLon) params.set('near_lon', mediaFilter.nearLon);
  const q = params.toString();
  return q ? `${prefix}${q}` : '';
}

function renderMedia(items) {
  mediaItems = items || [];
  const visibleIDs = new Set(mediaItems.map((item) => Number(item.id)));
  selectedIDs = new Set(Array.from(selectedIDs).filter((id) => visibleIDs.has(id)));

  mediaGrid.innerHTML = '';
  if (!mediaItems.length) {
    updateSelectionInfo();
    if (currentPreviewID && !visibleIDs.has(Number(currentPreviewID))) {
      clearPreview();
    }
    if (viewMode === 'albums' && activeAlbumID === 0) {
      mediaGrid.innerHTML = '<p>Select an album to view its media.</p>';
    } else {
      mediaGrid.innerHTML = '<p>No media found for this filter.</p>';
    }
    return;
  }

  mediaItems.forEach((item) => {
    const tile = document.createElement('article');
    tile.className = 'tile';
    const id = Number(item.id);
    if (selectedIDs.has(id)) {
      tile.classList.add('selected');
    }

    const mediaEl = item.kind === 'video'
      ? `<video muted preload="metadata" src="${item.preview_url}"></video>`
      : `<img loading="lazy" src="${item.preview_url}" alt="${escapeHtml(item.file_name)}"/>`;

    tile.innerHTML = `${mediaEl}
      <div class="meta">
        <div title="${escapeHtml(item.file_name)}">${escapeHtml(item.file_name)}</div>
        <div>${new Date(item.capture_time).toLocaleString()}</div>
        <div class="muted">${escapeHtml(item.location || '')}</div>
      </div>`;

    const select = document.createElement('label');
    select.className = 'tile-select';
    const checkbox = document.createElement('input');
    checkbox.type = 'checkbox';
    checkbox.checked = selectedIDs.has(id);
    checkbox.addEventListener('click', (event) => event.stopPropagation());
    checkbox.addEventListener('change', (event) => {
      event.stopPropagation();
      if (checkbox.checked) {
        selectedIDs.add(id);
        tile.classList.add('selected');
      } else {
        selectedIDs.delete(id);
        tile.classList.remove('selected');
      }
      updateSelectionInfo();
    });
    select.appendChild(checkbox);
    select.addEventListener('click', (event) => event.stopPropagation());
    tile.appendChild(select);

    tile.addEventListener('click', () => renderPreview(item));
    mediaGrid.appendChild(tile);
  });

  updateSelectionInfo();
  if (currentPreviewID && !visibleIDs.has(Number(currentPreviewID))) {
    clearPreview();
  }
}

function renderPreview(item) {
  currentPreviewID = Number(item.id);
  deleteCurrentBtn?.classList.remove('hidden');
  downloadCurrentBtn?.classList.remove('hidden');
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

function clearPreview() {
  currentPreviewID = null;
  if (deleteCurrentBtn) {
    deleteCurrentBtn.classList.add('hidden');
  }
  if (downloadCurrentBtn) {
    downloadCurrentBtn.classList.add('hidden');
  }
  if (previewPane) {
    previewPane.className = 'preview-empty';
    previewPane.textContent = 'Select a file from the album.';
  }
}

function updateSelectionInfo() {
  if (!selectionInfo) return;
  const count = selectedIDs.size;
  selectionInfo.textContent = count === 1 ? '1 selected' : `${count} selected`;
}

async function deleteSelectedMedia(ids) {
  const normalized = Array.from(new Set((ids || []).map((id) => Number(id)).filter((id) => Number.isFinite(id) && id > 0)));
  if (!normalized.length) {
    return;
  }

  const label = normalized.length === 1 ? '1 file' : `${normalized.length} files`;
  const confirmed = window.confirm(`Delete ${label}?\n\nThis removes files from storage and deletes their database records.`);
  if (!confirmed) return;

  try {
    const result = await api('/api/media/delete', {
      method: 'POST',
      body: { ids: normalized }
    });

    normalized.forEach((id) => selectedIDs.delete(id));
    if (currentPreviewID && normalized.includes(Number(currentPreviewID))) {
      clearPreview();
    }
    await loadDashboardData();
    statusChip.textContent = `Deleted ${result.deleted || 0} file(s), failed ${result.failed || 0}`;
  } catch (err) {
    statusChip.textContent = `Delete failed: ${err.message}`;
  }
}

function readMediaFilterControls() {
  mediaFilter.q = String(textFilterInput?.value || '').trim();
  mediaFilter.kind = String(kindFilterSelect?.value || '').trim().toLowerCase();
  mediaFilter.gps = String(gpsFilterSelect?.value || '').trim().toLowerCase();
  mediaFilter.from = normalizeDateToStartISO(captureFromInput?.value || '');
  mediaFilter.to = normalizeDateToEndISO(captureToInput?.value || '');
  mediaFilter.sort = String(sortBySelect?.value || 'capture_time').trim();
  mediaFilter.order = String(sortOrderSelect?.value || 'desc').trim().toLowerCase();
  mediaFilter.nearLat = String(nearLatInput?.value || '').trim();
  mediaFilter.nearLon = String(nearLonInput?.value || '').trim();
}

function writeMediaFilterControls() {
  if (textFilterInput) textFilterInput.value = mediaFilter.q || '';
  if (kindFilterSelect) kindFilterSelect.value = mediaFilter.kind || '';
  if (gpsFilterSelect) gpsFilterSelect.value = mediaFilter.gps || '';
  if (captureFromInput) captureFromInput.value = isoToDateValue(mediaFilter.from);
  if (captureToInput) captureToInput.value = isoToDateValue(mediaFilter.to);
  if (sortBySelect) sortBySelect.value = mediaFilter.sort || 'capture_time';
  if (sortOrderSelect) sortOrderSelect.value = mediaFilter.order || 'desc';
  if (nearLatInput) nearLatInput.value = mediaFilter.nearLat || '';
  if (nearLonInput) nearLonInput.value = mediaFilter.nearLon || '';
}

function normalizeDateToStartISO(raw) {
  const value = String(raw || '').trim();
  if (!value) return '';
  const dt = new Date(`${value}T00:00:00`);
  if (Number.isNaN(dt.getTime())) return '';
  return dt.toISOString();
}

function normalizeDateToEndISO(raw) {
  const value = String(raw || '').trim();
  if (!value) return '';
  const dt = new Date(`${value}T23:59:59`);
  if (Number.isNaN(dt.getTime())) return '';
  return dt.toISOString();
}

function isoToDateValue(raw) {
  const value = String(raw || '').trim();
  if (!value) return '';
  if (/^\d{4}-\d{2}-\d{2}$/.test(value)) return value;
  const dt = new Date(value);
  if (Number.isNaN(dt.getTime())) return '';
  const year = dt.getFullYear();
  const month = String(dt.getMonth() + 1).padStart(2, '0');
  const day = String(dt.getDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
}

function downloadSelectedAsFiles(ids) {
  const normalized = Array.from(new Set((ids || []).map((id) => Number(id)).filter((id) => Number.isFinite(id) && id > 0)));
  if (!normalized.length) return;

  const itemByID = new Map((mediaItems || []).map((item) => [Number(item.id), item]));
  let started = 0;

  normalized.forEach((id, idx) => {
    const item = itemByID.get(id);
    if (!item || !item.preview_url) return;
    started++;
    setTimeout(() => {
      const sep = String(item.preview_url).includes('?') ? '&' : '?';
      triggerDownload(`${item.preview_url}${sep}download=1`, item.file_name || `media-${id}`);
    }, idx * 120);
  });

  if (started > 0) {
    statusChip.textContent = started === 1 ? 'Starting 1 file download...' : `Starting ${started} file downloads...`;
  } else {
    statusChip.textContent = 'No downloadable files are visible in the current set.';
  }
}

async function downloadSelectedAsZip(ids) {
  const normalized = Array.from(new Set((ids || []).map((id) => Number(id)).filter((id) => Number.isFinite(id) && id > 0)));
  if (!normalized.length) return;

  try {
    const response = await fetch('/api/media/download-zip', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ids: normalized })
    });
    if (!response.ok) {
      const payload = await response.json().catch(() => ({}));
      throw new Error(payload.error || `ZIP download failed (${response.status})`);
    }

    const blob = await response.blob();
    const filename = parseFilenameFromContentDisposition(response.headers.get('Content-Disposition')) || `usbvault_export_${Date.now()}.zip`;
    const url = URL.createObjectURL(blob);
    triggerDownload(url, filename, true);
    statusChip.textContent = normalized.length === 1 ? 'ZIP download ready for 1 file.' : `ZIP download ready for ${normalized.length} files.`;
  } catch (err) {
    statusChip.textContent = `ZIP download failed: ${err.message}`;
  }
}

function triggerDownload(href, filename, revokeObjectURL = false) {
  const anchor = document.createElement('a');
  anchor.href = href;
  anchor.download = filename || 'download.bin';
  anchor.rel = 'noopener';
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  if (revokeObjectURL) {
    setTimeout(() => URL.revokeObjectURL(href), 1000);
  }
}

function parseFilenameFromContentDisposition(raw) {
  const value = String(raw || '');
  const match = value.match(/filename=\"?([^\";]+)\"?/i);
  if (!match) return '';
  return match[1].trim();
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

async function refreshBackupStatus() {
  if (!backupStatus) return;
  const st = await api('/api/backup-status');
  renderBackupStatus(st);
}

function renderBackupStatus(st) {
  if (!backupStatus) return;
  const state = String(st?.state || 'idle').toLowerCase();
  if (state === 'running') {
    const files = Number(st.files || 0);
    const mb = Number(st.bytes || 0) / (1024 * 1024);
    backupStatus.textContent = `Running: ${st.mode || 'backup'} -> ${st.destination || ''} | ${files} files | ${mb.toFixed(1)} MB`;
    return;
  }
  if (state === 'success') {
    backupStatus.textContent = `Completed: ${st.message || ''}`.trim();
    return;
  }
  if (state === 'error') {
    backupStatus.textContent = `Error: ${st.message || 'backup failed'}`;
    return;
  }
  backupStatus.textContent = st?.message || 'Backup idle.';
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
    refreshBackupStatus().catch(() => {});
  }, 1000);
  refreshIngestStatus().catch(() => {});
  refreshBackupStatus().catch(() => {});
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
