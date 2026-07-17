import { volumeMode, localVolume, localMuted, lastState, currentRoomID, localVolumeStorageKey, localMutedStorageKey, defaultVolume, storageGet, storageSet, setVolumeMode, setLocalVolume, setLocalMuted } from "./state.js";
import permissions from "./permissions.js";
import apiModule from "./api.js";


let muteButton, volumeInput, volumeModeButton;

function init() {
  muteButton = document.getElementById("mute");
  volumeInput = document.getElementById("volume");
  volumeModeButton = document.getElementById("volumeMode");

  volumeInput.addEventListener("input", () => {
    const next = Number(volumeInput.value);
    if (!Number.isFinite(next)) return;
    if (volumeMode === "room") {
      applyAudioSettings(next, false);
    } else {
      setLocalVolume(next);
      setLocalMuted(next === 0);
      storageSet(localVolumeStorageKey, localVolume);
      storageSet(localMutedStorageKey, localMuted);
      applyAudioSettings(localVolume, localMuted);
    }
  });

  volumeInput.addEventListener("change", async () => {
    if (volumeMode !== "room" || !permissions.hasRoomPermission("volume_control")) return;
    try {
      await apiModule.command({action: "room_audio", volume: Number(volumeInput.value), muted: false});
    } catch (err) {
      console.error(err);
      renderVolumeControl();
    }
  });

  volumeModeButton.addEventListener("click", () => {
    setVolumeMode(volumeMode === "room" ? "local" : "room");
    storageSet(volumeModeStorageKey(), volumeMode);
    renderVolumeControl();
  });

  muteButton.addEventListener("click", async () => {
    if (volumeMode === "room") {
      if (!permissions.hasRoomPermission("volume_control")) return;
      const roomAudio = lastState?.room_audio || {volume: defaultVolume, muted: false};
      const muted = !roomAudio.muted && roomAudio.volume > 0;
      const volume = !muted && roomAudio.volume === 0 ? defaultVolume : roomAudio.volume;
      await apiModule.command({action: "room_audio", volume, muted});
      return;
    }
    if (localMuted || localVolume === 0) {
      if (localVolume === 0) setLocalVolume(defaultVolume);
      setLocalMuted(false);
    } else {
      setLocalMuted(true);
    }
    storageSet(localVolumeStorageKey, localVolume);
    storageSet(localMutedStorageKey, localMuted);
    applyAudioSettings(localVolume, localMuted);
  });
}

function renderVolumeButton() {
  const muted = audioEl.muted || audioEl.volume === 0;
  muteButton.title = muted ? "Unmute" : "Mute";
  muteButton.setAttribute("aria-label", muted ? "Unmute" : "Mute");
  muteButton.classList.toggle("muted", muted);
}

let audioEl;

function applyAudioSettings(value, muted) {
  if (!audioEl) audioEl = document.getElementById("audio");
  const max = Number(volumeInput.max) || 1;
  audioEl.volume = Math.max(0, Math.min(max, value));
  audioEl.muted = Boolean(muted) || audioEl.volume === 0;
  volumeInput.value = String(audioEl.volume);
  renderVolumeButton();
}

function volumeModeStorageKey() {
  return `listen-party.volumeMode.${currentRoomID || "default"}`;
}

function restoreVolumePreferences() {
  if (!audioEl) audioEl = document.getElementById("audio");
  const storedVolume = Number(storageGet(localVolumeStorageKey));
  setLocalVolume(Number.isFinite(storedVolume) ? Math.max(0, Math.min(Number(volumeInput.max), storedVolume)) : 0);
  setLocalMuted(storageGet(localMutedStorageKey) === "true");
  setVolumeMode(storageGet(volumeModeStorageKey()) === "room" ? "room" : "local");
  renderVolumeControl();
}

function renderVolumeControl() {
  const roomMode = volumeMode === "room";
  const roomAudio = lastState?.room_audio || {volume: defaultVolume, muted: false};
  const canControlRoomVolume = permissions.hasRoomPermission("volume_control");
  volumeModeButton.textContent = roomMode ? "Room" : "Local";
  volumeModeButton.setAttribute("aria-pressed", String(roomMode));
  volumeModeButton.title = roomMode ? "Use local volume" : "Use room volume";
  volumeInput.disabled = roomMode && !canControlRoomVolume;
  muteButton.disabled = roomMode && !canControlRoomVolume;
  applyAudioSettings(roomMode ? roomAudio.volume : localVolume, roomMode ? roomAudio.muted : localMuted);
}

export default { init, renderVolumeButton, applyAudioSettings, volumeModeStorageKey, restoreVolumePreferences, renderVolumeControl };
