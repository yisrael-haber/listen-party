import {
  rescanStatus,
  scanStatus,
  scanStatusTimer,
  setScanStatusTimer,
  rescanButton,
} from "./state.js";
import { api } from "./api.js";

export function setRescanStatus(message, kind = "") {
  rescanStatus.textContent = message;
  rescanStatus.dataset.kind = kind;
}

export function formatScanTime(value) {
  if (!value || value === "0001-01-01T00:00:00Z") return "Never rescanned";
  return `Last rescanned ${new Date(value).toLocaleString()}`;
}

export function formatRate(value) {
  if (!Number.isFinite(value)) return "0/s";
  return `${value.toFixed(value >= 10 ? 0 : 1)}/s`;
}

export function shortPath(path) {
  return (path || "").split(/[\\/]/).filter(Boolean).pop() || path;
}

export function renderScanStatus(scan) {
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

export async function loadLibraryStatus() {
  clearTimeout(scanStatusTimer);
  try {
    const info = await api("/api/library");
    if (renderScanStatus(info.scan)) {
      setScanStatusTimer(setTimeout(() => {
        loadLibraryStatus().catch(console.error);
      }, 2000));
    }
  } catch (err) {
    scanStatus.textContent = "Scan status unavailable";
    scanStatus.dataset.kind = "error";
    console.error(err);
  }
}

export async function rescanMusicDir(path, button) {
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

export function init() {
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
}
