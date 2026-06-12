const configStatus = document.getElementById("configStatus");
const configForm = document.getElementById("configForm");
const configAddr = document.getElementById("configAddr");
const configMusicDirs = document.getElementById("configMusicDirs");
const configDatabasePath = document.getElementById("configDatabasePath");
const configScanWorkers = document.getElementById("configScanWorkers");
const configListenerUser = document.getElementById("configListenerUser");
const configListenerPass = document.getElementById("configListenerPass");
const configAdminUser = document.getElementById("configAdminUser");
const configAdminPass = document.getElementById("configAdminPass");
const rescanButton = document.getElementById("rescan");
const rescanStatus = document.getElementById("rescanStatus");
const scanStatus = document.getElementById("scanStatus");
let scanStatusTimer = 0;

function setStatus(message, kind = "") {
  configStatus.textContent = message;
  configStatus.dataset.kind = kind;
}

function renderConfig(cfg) {
  configAddr.value = cfg.addr || "";
  configMusicDirs.value = (cfg.music_dirs || []).join("\n");
  configDatabasePath.value = cfg.database_path || "";
  configScanWorkers.value = cfg.scan_workers || 16;
  configListenerUser.value = cfg.auth.listener.username || "";
  configListenerPass.value = cfg.auth.listener.password || "";
  configAdminUser.value = cfg.auth.admin.username || "";
  configAdminPass.value = cfg.auth.admin.password || "";
}

function readConfigForm() {
  return {
    addr: configAddr.value.trim(),
    music_dirs: configMusicDirs.value.split("\n").map((dir) => dir.trim()).filter(Boolean),
    database_path: configDatabasePath.value.trim(),
    scan_workers: Math.max(1, Math.min(256, Math.floor(Number(configScanWorkers.value) || 16))),
    auth: {
      listener: {username: configListenerUser.value.trim(), password: configListenerPass.value},
      admin: {username: configAdminUser.value.trim(), password: configAdminPass.value},
    },
  };
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
    setStatus(view.restart_needed ? "Saved; restart required for address or database changes" : "Saved", "ok");
  } catch (err) {
    setStatus("Save failed", "error");
    console.error(err);
  }
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
