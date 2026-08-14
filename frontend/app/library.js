import { scanPollActiveMS, scanPollIdleMS } from "./state.js";
import formatting from "./formatting.js";
import apiModule from "./api.js";
import searchModule from "./search.js";
import albums from "./albums.js";

let libraryStatus, rescanButton, scanProgress, scanProgressStage, scanProgressDetail;
let statusPollTimer = 0;
let rescanResetTimer = 0;
let canRestartScan = false;
let wasScanning = false;

function init() {
  libraryStatus = document.getElementById("libraryStatus");
  rescanButton = document.getElementById("rescanLibrary");
  scanProgress = document.getElementById("scanProgress");
  scanProgressStage = document.getElementById("scanProgressStage");
  scanProgressDetail = document.getElementById("scanProgressDetail");

  rescanButton.addEventListener("click", () => {
    if (!confirmRescan()) {
      return;
    }
    startRescan().catch(console.error);
  });
}

function confirmRescan() {
  if (wasScanning) {
    return confirm(
      "A resync is already running. Restart it from the beginning?",
    );
  }
  return confirm(
    "Resync the music library? Every configured music directory is reindexed, and nobody else can start a resync until it finishes.",
  );
}

async function startRescan() {
  setRescanState("working");
  try {
    await apiModule.api("/api/library/rescan", { method: "POST" });
    wasScanning = true;
    scheduleStatusPoll(400);
  } catch (err) {
    setRescanState("error", err.message);
    scheduleStatusPoll(scanPollIdleMS);
  }
}

async function loadLibraryStatus() {
  try {
    const info = await apiModule.api("/api/library");
    canRestartScan = Boolean(info.can_restart_scan);
    libraryStatus.textContent = `${info.track_count} tracks indexed`;
    renderScan(info.scan || {});
    return info;
  } catch (err) {
    libraryStatus.textContent = "Library status unavailable";
    setRescanState("error", err.message);
    return null;
  }
}

function renderScan(scan) {
  const scanning = Boolean(scan.scanning);
  renderScanProgress(scan, scanning);
  if (scanning) {
    setRescanState(canRestartScan ? "restartable" : "working");
  } else if (wasScanning) {
    setRescanState(scan.last_error ? "error" : "idle", scan.last_error);
    searchModule.runSearch().catch(console.error);
    albums.loadAlbums().catch(console.error);
  }
  wasScanning = scanning;
}

function renderScanProgress(scan, scanning) {
  scanProgress.hidden = !scanning;
  if (!scanning) {
    return;
  }
  scanProgressStage.textContent = scan.stage || "starting";
  const counts = [
    `${scan.files_seen || 0} seen`,
    `${scan.indexed || 0} indexed`,
    `${scan.unchanged || 0} unchanged`,
  ];
  if (scan.skipped > 0) {
    counts.push(`${scan.skipped} skipped`);
  }
  if (scan.removed > 0) {
    counts.push(`${scan.removed} missing`);
  }
  const elapsed = scan.duration_ms > 0 ? scan.duration_ms / 1000 : 0;
  scanProgressDetail.textContent = `${counts.join(" · ")} · ${formatting.formatTime(elapsed)}`;
}

function setRescanState(state, detail = "") {
  clearTimeout(rescanResetTimer);
  rescanButton.disabled = state === "working";
  if (state === "working") {
    rescanButton.textContent = "Syncing...";
    rescanButton.title = "A resync is running; the library is being reindexed";
    return;
  }
  if (state === "restartable") {
    rescanButton.textContent = "Restart";
    rescanButton.title = "Restart the running resync from the beginning";
    return;
  }
  if (state === "error") {
    rescanButton.textContent = "Failed";
    rescanButton.title = detail?.trim() || "Resync failed";
    rescanResetTimer = setTimeout(() => setRescanState("idle"), 6000);
    return;
  }
  rescanButton.textContent = "Resync";
  rescanButton.title = "Resync the configured music directories";
}

function scheduleStatusPoll(delay = scanPollIdleMS) {
  clearTimeout(statusPollTimer);
  statusPollTimer = setTimeout(async () => {
    const info = await loadLibraryStatus();
    scheduleStatusPoll(
      info?.scan?.scanning ? scanPollActiveMS : scanPollIdleMS,
    );
  }, delay);
}

export default { init, loadLibraryStatus, scheduleStatusPoll };
