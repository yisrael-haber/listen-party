const configStatus = document.getElementById("configStatus");
const configForm = document.getElementById("configForm");
const configAddr = document.getElementById("configAddr");
const configMusicDirs = document.getElementById("configMusicDirs");
const configDatabasePath = document.getElementById("configDatabasePath");
const configScanWorkers = document.getElementById("configScanWorkers");
const configAuthDataDir = document.getElementById("configAuthDataDir");
const configBootstrapEmail = document.getElementById("configBootstrapEmail");
const configKeycloakEnabled = document.getElementById("configKeycloakEnabled");
const configKeycloakIssuer = document.getElementById("configKeycloakIssuer");
const configKeycloakClientID = document.getElementById("configKeycloakClientID");
const configKeycloakClientSecret = document.getElementById("configKeycloakClientSecret");
const configKeycloakDisplayName = document.getElementById("configKeycloakDisplayName");
const addRoomButton = document.getElementById("addRoom");
const roomsList = document.getElementById("roomsList");
const rescanButton = document.getElementById("rescan");
const rescanStatus = document.getElementById("rescanStatus");
const scanStatus = document.getElementById("scanStatus");
let scanStatusTimer = 0;
let roomCounter = 1;

function setStatus(message, kind = "") {
  configStatus.textContent = message;
  configStatus.dataset.kind = kind;
}

function renderConfig(cfg) {
  const auth = cfg.auth?.pocketbase || {};
  const keycloak = auth.keycloak || {};
  configAddr.value = cfg.addr || "";
  configMusicDirs.value = (cfg.music_dirs || []).join("\n");
  configDatabasePath.value = cfg.database_path || "";
  configScanWorkers.value = cfg.scan_workers || 16;
  configAuthDataDir.value = auth.data_dir || "";
  configBootstrapEmail.value = auth.bootstrap_admin_email || "";
  configKeycloakEnabled.checked = Boolean(keycloak.enabled);
  configKeycloakIssuer.value = keycloak.issuer_url || "";
  configKeycloakClientID.value = keycloak.client_id || "";
  configKeycloakClientSecret.value = keycloak.client_secret || "";
  configKeycloakDisplayName.value = keycloak.display_name || "Keycloak";
  renderRooms(cfg.rooms || [{id: "public", name: "Public Room", public: true}]);
}

function splitList(value) {
  return value.split(",").map((item) => item.trim()).filter(Boolean);
}

function readConfigForm() {
  return {
    addr: configAddr.value.trim(),
    music_dirs: configMusicDirs.value.split("\n").map((dir) => dir.trim()).filter(Boolean),
    database_path: configDatabasePath.value.trim(),
    scan_workers: Math.max(1, Math.min(256, Math.floor(Number(configScanWorkers.value) || 16))),
    rooms: readRooms(),
    auth: {
      pocketbase: {
        data_dir: configAuthDataDir.value.trim(),
        bootstrap_admin_email: configBootstrapEmail.value.trim(),
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

function renderRooms(rooms) {
  roomsList.replaceChildren(...rooms.map(renderRoomRow));
  updateRoomRemoveButtons();
}

function renderRoomRow(room = {}) {
  const row = document.createElement("div");
  row.className = "room-row";

  const id = inputField("ID", "room-id", room.id || `room-${roomCounter++}`);
  const name = inputField("Name", "room-name", room.name || "New Room");
  const roles = inputField("Allowed roles", "room-roles", (room.allowed_roles || []).join(", "));
  const groups = inputField("Allowed groups", "room-groups", (room.allowed_groups || []).join(", "));

  const publicLabel = document.createElement("label");
  publicLabel.className = "checkbox-label room-public";
  const publicInput = document.createElement("input");
  publicInput.className = "room-public-input";
  publicInput.type = "checkbox";
  publicInput.checked = Boolean(room.public);
  publicLabel.append(publicInput, document.createElement("span"));
  publicLabel.lastElementChild.textContent = "Public";

  const remove = document.createElement("button");
  remove.className = "secondary compact room-remove";
  remove.type = "button";
  remove.textContent = "Remove";
  remove.addEventListener("click", () => {
    row.remove();
    updateRoomRemoveButtons();
  });

  row.append(id, name, publicLabel, roles, groups, remove);
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
    public: row.querySelector(".room-public-input").checked,
    allowed_roles: splitList(row.querySelector(".room-roles").value),
    allowed_groups: splitList(row.querySelector(".room-groups").value),
  }));
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
    const view = await api("/api/admin/config");
    renderConfig(view.config);
    setStatus(view.path ? `Loaded ${view.path}` : "Loaded", "ok");
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

function renderScanStatus(scan) {
  if (!scan) {
    scanStatus.textContent = "";
    scanStatus.dataset.kind = "";
    return false;
  }
  if (scan.scanning) {
    scanStatus.textContent = `Scanning ${scan.mp3_seen || 0} seen, ${scan.indexed || 0} indexed, ${scan.unchanged || 0} unchanged, ${formatRate(scan.recent_tracks_per_sec)} recent`;
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
  setStatus("Saving...", "working");
  try {
    const view = await api("/api/admin/config", {
      method: "PUT",
      body: JSON.stringify(readConfigForm()),
    });
    renderConfig(view.config);
    setStatus(view.restart_needed ? "Saved; restart required for address, database, or auth changes" : "Saved", "ok");
  } catch (err) {
    setStatus("Save failed", "error");
    console.error(err);
  }
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

loadConfig();
loadLibraryStatus();
