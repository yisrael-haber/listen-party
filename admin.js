const configStatus = document.getElementById("configStatus");
const configForm = document.getElementById("configForm");
const configAddr = document.getElementById("configAddr");
const configMusicDirs = document.getElementById("configMusicDirs");
const configDatabasePath = document.getElementById("configDatabasePath");
const configListenerUser = document.getElementById("configListenerUser");
const configListenerPass = document.getElementById("configListenerPass");
const configAdminUser = document.getElementById("configAdminUser");
const configAdminPass = document.getElementById("configAdminPass");
const rescanButton = document.getElementById("rescan");
const rescanStatus = document.getElementById("rescanStatus");

function setStatus(message, kind = "") {
  configStatus.textContent = message;
  configStatus.dataset.kind = kind;
}

function renderConfig(cfg) {
  configAddr.value = cfg.addr || "";
  configMusicDirs.value = (cfg.music_dirs || []).join("\n");
  configDatabasePath.value = cfg.database_path || "";
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
    if (!res.ok) throw new Error(await res.text());
    setRescanStatus("Library rescanned", "ok");
  } catch (err) {
    setRescanStatus("Rescan failed", "error");
    console.error(err);
  } finally {
    rescanButton.disabled = false;
  }
});

loadConfig();
