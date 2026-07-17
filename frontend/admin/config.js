import {
  configStatus,
  configForm,
  configSaveButton,
  configAddr,
  configScanWorkers,
  configKeycloakEnabled,
  configKeycloakIssuer,
  configKeycloakClientID,
  configKeycloakClientSecret,
  configKeycloakDisplayName,
  configRevision,
  setConfigRevision,
  saveFeedbackTimer,
  setSaveFeedbackTimer,
  minimumSaveFeedbackMS,
} from "./state.js";
import { api, delay } from "./api.js";
import { renderMusicDirs, readMusicDirs } from "./music-dirs.js";
import { renderBannedIPs, readBannedIPs } from "./banned-ips.js";
import { renderRooms, readRooms } from "./rooms.js";

export function setStatus(message, kind = "") {
  configStatus.textContent = message;
  configStatus.dataset.kind = kind;
}

export function resetSaveButtonAfterDelay() {
  clearTimeout(saveFeedbackTimer);
  setSaveFeedbackTimer(setTimeout(() => {
    configSaveButton.textContent = "Save";
    configSaveButton.dataset.state = "";
  }, 1400));
}

export function renderConfig(cfg) {
  setConfigRevision(cfg.revision || 1);
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

export function readConfigForm() {
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

export async function loadConfig() {
  setStatus("Loading...", "working");
  try {
    renderConfig(await api("/api/admin/config"));
    setStatus("Loaded", "ok");
  } catch (err) {
    setStatus("Could not load config", "error");
    console.error(err);
  }
}

export function init() {
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
}
