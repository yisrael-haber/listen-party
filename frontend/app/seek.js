import { seeking, lastState, setSeeking } from "./state.js";
import audio from "./audio.js";
import permissions from "./permissions.js";
import formatting from "./formatting.js";
import apiModule from "./api.js";


let seekInput, elapsedEl, durationEl;

function init() {
  seekInput = document.getElementById("seek");
  elapsedEl = document.getElementById("elapsed");
  durationEl = document.getElementById("duration");

  seekInput.addEventListener("input", () => {
    setSeeking(true);
    setSeekUI(Number(seekInput.value));
  });

  seekInput.addEventListener("change", async () => {
    if (!audio.hasMedia()) {
      setSeeking(false);
      setSeekUI(0);
      return;
    }
    const positionMS = Math.max(0, Math.round(Number(seekInput.value) * 1000));
    setSeeking(false);
    await apiModule.command({action: "seek", position_ms: positionMS});
  });
}

function setSeekUI(position) {
  const duration = audio.mediaDuration();
  const max = duration > 0 ? duration : Math.max(position, 0);
  const value = Math.min(position, max);
  seekInput.max = String(Math.ceil(max));
  seekInput.disabled = !audio.hasMedia() || !permissions.hasRoomPermission("playback_control");
  if (!seeking) {
    seekInput.value = String(value);
  }
  elapsedEl.textContent = formatting.formatTime(seeking ? Number(seekInput.value) : value);
  durationEl.textContent = formatting.formatTime(duration);
}

function renderPlaybackButton(playing) {
  const togglePlaybackButton = document.getElementById("togglePlayback");
  togglePlaybackButton.title = playing ? "Pause" : "Play";
  togglePlaybackButton.setAttribute("aria-label", playing ? "Pause" : "Play");
  togglePlaybackButton.firstElementChild.className = `playback-icon ${playing ? "pause-icon" : "play-icon"}`;
}

export default { init, setSeekUI, renderPlaybackButton };
