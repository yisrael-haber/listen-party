const configStatus = document.getElementById("configStatus");
const configForm = document.getElementById("configForm");
const configSaveButton = document.getElementById("configSave");
const configAddr = document.getElementById("configAddr");
const configMusicDirs = document.getElementById("configMusicDirs");
const configBannedIPs = document.getElementById("configBannedIPs");
const configScanWorkers = document.getElementById("configScanWorkers");
const configKeycloakEnabled = document.getElementById("configKeycloakEnabled");
const configKeycloakIssuer = document.getElementById("configKeycloakIssuer");
const configKeycloakClientID = document.getElementById("configKeycloakClientID");
const configKeycloakClientSecret = document.getElementById("configKeycloakClientSecret");
const configKeycloakDisplayName = document.getElementById("configKeycloakDisplayName");
const addMusicDirButton = document.getElementById("addMusicDir");
const addBannedIPButton = document.getElementById("addBannedIP");
const addRoomButton = document.getElementById("addRoom");
const roomsList = document.getElementById("roomsList");
const rescanButton = document.getElementById("rescan");
const rescanStatus = document.getElementById("rescanStatus");
const scanStatus = document.getElementById("scanStatus");
let scanStatusTimer = 0;
let saveFeedbackTimer = 0;
let roomCounter = 1;
let configRevision = 0;
const minimumSaveFeedbackMS = 350;

function setStatus(message, kind = "") {
  configStatus.textContent = message;
  configStatus.dataset.kind = kind;
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function resetSaveButtonAfterDelay() {
  clearTimeout(saveFeedbackTimer);
  saveFeedbackTimer = setTimeout(() => {
    configSaveButton.textContent = "Save";
    configSaveButton.dataset.state = "";
  }, 1400);
}

function renderConfig(cfg) {
  configRevision = cfg.revision || 1;
  const auth = cfg.auth?.pocketbase || {};
  const keycloak = auth.keycloak || {};
  configAddr.value = cfg.addr || "";
  renderMusicDirs(cfg.music_dirs || []);
  renderBannedIPs(cfg.banned_ips || []);
  configScanWorkers.value = cfg.scan_workers || 16;
  configKeycloakEnabled.checked = Boolean(keycloak.enabled);
  configKeycloakIssuer.value = keycloak.issuer_url || "";
  configKeycloakClientID.value = keycloak.client_id || "";
  configKeycloakClientSecret.value = keycloak.client_secret || "";
  configKeycloakDisplayName.value = keycloak.display_name || "Keycloak";
  renderRooms(cfg.rooms || [{
    id: "main",
    name: "Public Room",
    grants: {everyone: ["queue_add", "queue_manage", "playback_control"]},
  }]);
}

function readConfigForm() {
  return {
    revision: configRevision,
    addr: configAddr.value.trim(),
    music_dirs: readMusicDirs(),
    banned_ips: readBannedIPs(),
    scan_workers: Math.max(1, Math.min(256, Math.floor(Number(configScanWorkers.value) || 16))),
    rooms: readRooms(),
    auth: {
      pocketbase: {
        keycloak: {
          enabled: configKeycloakEnabled.checked,
          issuer_url: configKeycloakIssuer.value.trim(),
          client_id: configKeycloakClientID.value.trim(),
          client_secret: configKeycloakClientSecret.value,
          display_name: configKeycloakDisplayName.value.trim() || "Keycloak",
        },
      },
    },
  };
}

function renderMusicDirs(paths) {
  const rows = paths.length > 0 ? paths : [""];
  configMusicDirs.replaceChildren(...rows.map(renderMusicDirItem));
  updateListRemoveButtons(configMusicDirs);
}

function renderMusicDirItem(path) {
  const row = renderListItem(path, "music-dir-input", "/path/to/music", "Music directory");
  row.classList.add("music-dir-item");
  const rescan = document.createElement("button");
  rescan.className = "secondary compact path-rescan";
  rescan.type = "button";
  rescan.textContent = "Rescan";
  rescan.addEventListener("click", async () => {
    await rescanMusicDir(row.querySelector(".music-dir-input").value.trim(), rescan);
  });
  row.insertBefore(rescan, row.lastElementChild);
  return row;
}

function renderBannedIPs(ips) {
  const rows = ips.length > 0 ? ips : [""];
  configBannedIPs.replaceChildren(...rows.map((ip) => renderListItem(ip, "banned-ip-input", "192.168.1.50", "Banned IP address")));
  updateListRemoveButtons(configBannedIPs);
}

function renderListItem(value, inputClass, placeholder, ariaLabel) {
  const row = document.createElement("div");
  row.className = "list-editor-item";

  const input = document.createElement("input");
  input.className = inputClass;
  input.value = value;
  input.autocomplete = "off";
  input.spellcheck = false;
  input.placeholder = placeholder;
  input.setAttribute("aria-label", ariaLabel);

  const remove = document.createElement("button");
  remove.className = "secondary compact icon-only trash-button list-editor-remove";
  remove.type = "button";
  remove.title = "Remove";
  remove.setAttribute("aria-label", "Remove");
  remove.append(document.createElement("span"));
  remove.addEventListener("click", () => {
    const list = row.parentElement;
    row.remove();
    updateListRemoveButtons(list);
  });

  row.append(input, remove);
  return row;
}

function addMusicDir(path = "") {
  const row = renderMusicDirItem(path);
  configMusicDirs.append(row);
  updateListRemoveButtons(configMusicDirs);
  row.querySelector(".music-dir-input").focus();
}

function addBannedIP(ip = "") {
  const row = renderListItem(ip, "banned-ip-input", "192.168.1.50", "Banned IP address");
  configBannedIPs.append(row);
  updateListRemoveButtons(configBannedIPs);
  row.querySelector(".banned-ip-input").focus();
}

function readMusicDirs() {
  return [...configMusicDirs.querySelectorAll(".music-dir-input")].map((input) => input.value.trim()).filter(Boolean);
}

function readBannedIPs() {
  return [...configBannedIPs.querySelectorAll(".banned-ip-input")].map((input) => input.value.trim()).filter(Boolean);
}

function updateListRemoveButtons(container) {
  const buttons = container.querySelectorAll(".list-editor-remove");
  buttons.forEach((button) => {
    button.disabled = buttons.length <= 1;
  });
}

function listEditor(title, inputClass, values, placeholder) {
  const editor = document.createElement("div");
  editor.className = "list-editor room-list-editor";

  const head = document.createElement("div");
  head.className = "list-editor-head";
  const label = document.createElement("span");
  label.textContent = title;
  const add = document.createElement("button");
  add.className = "secondary compact";
  add.type = "button";
  add.textContent = "Add";
  head.append(label, add);

  const list = document.createElement("div");
  list.className = "list-editor-items";
  const rows = values.length > 0 ? values : [""];
  list.replaceChildren(...rows.map((value) => renderListItem(value, inputClass, placeholder, title)));

  add.addEventListener("click", () => {
    const row = renderListItem("", inputClass, placeholder, title);
    list.append(row);
    updateListRemoveButtons(list);
    row.querySelector(`.${inputClass}`).focus();
  });

  editor.append(head, list);
  updateListRemoveButtons(list);
  return editor;
}

function renderRooms(rooms) {
  roomsList.replaceChildren(...rooms.map(renderRoomRow));
  updateRoomRemoveButtons();
}

function renderRoomRow(room = {}) {
  const row = document.createElement("div");
  row.className = "room-row";
  row.roomGrants = cloneGrants(room.grants || {});
  const fields = document.createElement("div");
  fields.className = "room-fields";
  const main = document.createElement("div");
  main.className = "room-main-row";
  const access = document.createElement("div");
  access.className = "room-access-row";

  const id = inputField("ID", "room-id", room.id || `room-${roomCounter++}`);
  const name = inputField("Name", "room-name", room.name || "New Room");

  const remove = document.createElement("button");
  remove.className = "secondary compact icon-only trash-button room-remove";
  remove.type = "button";
  remove.title = "Remove room";
  remove.setAttribute("aria-label", "Remove room");
  remove.append(document.createElement("span"));
  remove.addEventListener("click", () => {
    row.remove();
    updateRoomRemoveButtons();
  });

  main.append(id, name);
  access.append(listEditor("Room administrator groups", "room-admin-group", room.admin_groups || [], "Group"));
  fields.append(main, access);
  row.append(fields, remove);
  return row;
}

function inputField(labelText, className, value) {
  const label = document.createElement("label");
  label.className = "room-field";
  const span = document.createElement("span");
  span.textContent = labelText;
  const input = document.createElement("input");
  input.className = className;
  input.value = value;
  input.autocomplete = "off";
  label.append(span, input);
  return label;
}

function updateRoomRemoveButtons() {
  const buttons = roomsList.querySelectorAll(".room-remove");
  buttons.forEach((button) => {
    button.disabled = buttons.length <= 1;
  });
}

function readRooms() {
  return [...roomsList.querySelectorAll(".room-row")].map((row) => ({
    id: row.querySelector(".room-id").value.trim(),
    name: row.querySelector(".room-name").value.trim(),
    admin_groups: [...row.querySelectorAll(".room-admin-group")].map((input) => input.value.trim()).filter(Boolean),
    grants: cloneGrants(row.roomGrants),
  }));
}

function cloneGrants(grants) {
  return Object.fromEntries(Object.entries(grants || {}).map(([group, permissions]) => [group, [...permissions]]));
}

async function api(path, options = {}) {
  const res = await fetch(path, {
    headers: {"Content-Type": "application/json"},
    ...options,
  });
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

async function loadConfig() {
  setStatus("Loading...", "working");
  try {
    renderConfig(await api("/api/admin/config"));
    setStatus("Loaded", "ok");
  } catch (err) {
    setStatus("Could not load config", "error");
    console.error(err);
  }
}

function setRescanStatus(message, kind = "") {
  rescanStatus.textContent = message;
  rescanStatus.dataset.kind = kind;
}

function formatScanTime(value) {
  if (!value || value === "0001-01-01T00:00:00Z") return "Never rescanned";
  return `Last rescanned ${new Date(value).toLocaleString()}`;
}

function formatRate(value) {
  if (!Number.isFinite(value)) return "0/s";
  return `${value.toFixed(value >= 10 ? 0 : 1)}/s`;
}

function shortPath(path) {
  return (path || "").split(/[\\/]/).filter(Boolean).pop() || path;
}

function renderScanStatus(scan) {
  if (!scan) {
    scanStatus.textContent = "";
    scanStatus.dataset.kind = "";
    return false;
  }
  if (scan.scanning) {
    const roots = scan.roots || [];
    const scope = roots.length === 1 ? `Scanning ${shortPath(roots[0])}` : `Scanning ${roots.length || 0} folders`;
    scanStatus.textContent = `${scope}: ${scan.mp3_seen || 0} seen, ${scan.indexed || 0} indexed, ${scan.unchanged || 0} unchanged, ${formatRate(scan.recent_tracks_per_sec)} recent`;
    scanStatus.dataset.kind = "working";
    return true;
  }
  const base = formatScanTime(scan.last_completed);
  scanStatus.textContent = scan.last_error ? `${base}; last scan failed` : base;
  scanStatus.dataset.kind = scan.last_error ? "error" : "ok";
  return false;
}

async function loadLibraryStatus() {
  clearTimeout(scanStatusTimer);
  try {
    const info = await api("/api/library");
    if (renderScanStatus(info.scan)) {
      scanStatusTimer = setTimeout(() => {
        loadLibraryStatus().catch(console.error);
      }, 2000);
    }
  } catch (err) {
    scanStatus.textContent = "Scan status unavailable";
    scanStatus.dataset.kind = "error";
    console.error(err);
  }
}

configForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  if (configSaveButton.disabled) return;
  clearTimeout(saveFeedbackTimer);
  configSaveButton.disabled = true;
  configSaveButton.textContent = "Saving...";
  configSaveButton.dataset.state = "working";
  setStatus("Saving...", "working");
  try {
    const saveRequest = api("/api/admin/config", {
      method: "PUT",
      body: JSON.stringify(readConfigForm()),
    }).then((value) => ({value}), (error) => ({error}));
    const [result] = await Promise.all([saveRequest, delay(minimumSaveFeedbackMS)]);
    if (result.error) throw result.error;
    renderConfig(result.value);
    setStatus("Saved", "ok");
    configSaveButton.textContent = "Saved";
    configSaveButton.dataset.state = "saved";
  } catch (err) {
    setStatus((err.message || "Save failed").trim(), "error");
    configSaveButton.textContent = "Save failed";
    configSaveButton.dataset.state = "error";
    console.error(err);
  } finally {
    configSaveButton.disabled = false;
    resetSaveButtonAfterDelay();
  }
});

addMusicDirButton.addEventListener("click", () => {
  addMusicDir();
});

addBannedIPButton.addEventListener("click", () => {
  addBannedIP();
});

addRoomButton.addEventListener("click", () => {
  roomsList.append(renderRoomRow());
  updateRoomRemoveButtons();
});

rescanButton.addEventListener("click", async () => {
  rescanButton.disabled = true;
  setRescanStatus("Rescanning...", "working");
  try {
    const res = await fetch("/api/admin/rescan", {method: "POST"});
    if (res.status === 409) {
      setRescanStatus("Scan already in progress", "working");
      await loadLibraryStatus();
      return;
    }
    if (!res.ok) throw new Error(await res.text());
    setRescanStatus("Library rescanned", "ok");
    await loadLibraryStatus();
  } catch (err) {
    setRescanStatus("Rescan failed", "error");
    console.error(err);
  } finally {
    rescanButton.disabled = false;
  }
});

async function rescanMusicDir(path, button) {
  if (!path) {
    setRescanStatus("Choose a configured path first", "error");
    return;
  }
  button.disabled = true;
  setRescanStatus("Rescanning folder...", "working");
  try {
    const res = await fetch("/api/admin/rescan-dir", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({music_dir: path}),
    });
    if (res.status === 409) {
      setRescanStatus("Scan already in progress", "working");
      await loadLibraryStatus();
      return;
    }
    if (!res.ok) throw new Error(await res.text());
    setRescanStatus("Folder rescanned", "ok");
    await loadLibraryStatus();
  } catch (err) {
    setRescanStatus("Folder rescan failed", "error");
    console.error(err);
  } finally {
    button.disabled = false;
  }
}

loadConfig();
loadLibraryStatus();
