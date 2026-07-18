import {
  lastState,
  lastStateReceivedAt,
  seeking,
  recoveryStorageKey,
  recoveryCooldownMS,
  syncToleranceSeconds,
  events,
  currentRoomID,
  setEvents,
} from "./state.js";
import seek from "./seek.js";
import permissions from "./permissions.js";
import volume from "./volume.js";
import apiModule from "./api.js";

let audioEl, artworkEl, previousButton, skipButton, togglePlaybackButton;

function init() {
  audioEl = document.getElementById("audio");
  artworkEl = document.getElementById("artwork");
  previousButton = document.getElementById("previous");
  skipButton = document.getElementById("skip");
  togglePlaybackButton = document.getElementById("togglePlayback");

  artworkEl.addEventListener("load", () => {
    artworkEl.hidden = false;
  });

  artworkEl.addEventListener("error", clearArtwork);

  for (const eventName of ["loadedmetadata", "canplay"]) {
    audioEl.addEventListener(eventName, syncCurrentAudio);
  }

  document.addEventListener("visibilitychange", () => {
    if (!document.hidden) {
      syncCurrentAudio();
    }
  });

  audioEl.addEventListener("timeupdate", () => {
    if (!seeking && hasMedia()) {
      seek.setSeekUI(audioEl.currentTime);
    }
  });

  audioEl.addEventListener("volumechange", volume.renderVolumeButton);

  previousButton.addEventListener("click", async () => {
    await apiModule.command({ action: "previous" });
  });

  skipButton.addEventListener("click", async () => {
    await apiModule.command({ action: "skip" });
  });

  togglePlaybackButton.addEventListener("click", async () => {
    if (lastState && lastState.current && !lastState.paused) {
      await apiModule.command({ action: "pause" });
      return;
    }
    await apiModule.command({ action: "play" });
  });
}

function hasMedia() {
  return audioEl.hasAttribute("src");
}

function mediaURL(track, suffix = "") {
  if (!track || !track.id) {
    return "";
  }
  return `/media/${track.id}${suffix}?v=${encodeURIComponent(track.dedupe_key || "")}`;
}

function loadMedia(track) {
  const src = mediaURL(track);
  if (!src) {
    audioEl.removeAttribute("src");
    audioEl.load();
    clearArtwork();
    return;
  }
  if (audioEl.getAttribute("src") === src) {
    return;
  }
  audioEl.src = src;
  audioEl.load();
  loadArtwork(track);
}

function clearArtwork() {
  artworkEl.hidden = true;
  artworkEl.removeAttribute("src");
}

function loadArtwork(track) {
  const url = mediaURL(track, "/artwork");
  artworkEl.hidden = true;
  artworkEl.src = url || "";
}

function samePlaybackTimeline(a, b) {
  return Boolean(
    a &&
    b &&
    a.current?.dedupe_key === b.current?.dedupe_key &&
    a.started_at === b.started_at &&
    a.paused === b.paused &&
    a.position_at_pause_ms === b.position_at_pause_ms,
  );
}

function mediaDuration() {
  if (Number.isFinite(audioEl.duration) && audioEl.duration > 0) {
    return audioEl.duration;
  }
  const indexedMS = lastState?.current?.track?.duration_ms || 0;
  return indexedMS > 0 ? indexedMS / 1000 : 0;
}

function playbackPosition(state) {
  if (!state.started_at) {
    return 0;
  }
  if (state.paused) {
    return Math.max(0, state.position_at_pause_ms / 1000);
  }
  const serverNow = Date.parse(state.server_time);
  const startedAt = Date.parse(state.started_at);
  const localElapsed = Math.max(0, Date.now() - lastStateReceivedAt);
  return Math.max(0, (serverNow - startedAt + localElapsed) / 1000);
}

function setSyncedTime(target) {
  if (!Number.isFinite(target)) {
    return;
  }
  if (audioEl.readyState < HTMLMediaElement.HAVE_METADATA) {
    return;
  }
  if (Math.abs(audioEl.currentTime - target) > syncToleranceSeconds) {
    try {
      audioEl.currentTime = target;
    } catch (err) {
      console.warn("could not seek synchronized media yet", err);
    }
  }
}

function playAudio() {
  if (!hasMedia()) {
    return;
  }
  audioEl.play().catch((err) => {
    console.warn("browser refused synchronized playback", err);
  });
}

function syncAudio(state, correctTime = true) {
  if (!state.started_at) {
    seek.setSeekUI(0);
    return;
  }
  const target = playbackPosition(state);
  const duration = mediaDuration();
  if (!state.paused && duration > 0 && target > duration) {
    seek.setSeekUI(duration);
    return;
  }
  if (state.paused) {
    seek.setSeekUI(target);
    if (correctTime) {
      setSyncedTime(target);
    }
    if (!audioEl.paused) {
      audioEl.pause();
    }
    return;
  }

  if (correctTime) {
    setSyncedTime(target);
  }
  if (audioEl.paused) {
    playAudio();
  }
  seek.setSeekUI(
    audioEl.readyState >= HTMLMediaElement.HAVE_METADATA
      ? audioEl.currentTime
      : target,
  );
}

function syncCurrentAudio() {
  if (lastState && hasMedia()) {
    syncAudio(lastState);
  }
}

function recoverPlaybackClient(reason, error = null) {
  console.error(reason, error || "");
  closeEvents();
  audioEl.pause();
  try {
    const previous = Number(sessionStorage.getItem(recoveryStorageKey)) || 0;
    if (Date.now() - previous > recoveryCooldownMS) {
      sessionStorage.setItem(recoveryStorageKey, String(Date.now()));
      location.reload();
      return;
    }
  } catch {
    // Without storage, do not risk a refresh loop.
  }
  document.getElementById("libraryStatus").textContent =
    "Playback synchronization failed. Refresh this page.";
}

function closeEvents() {
  events?.close();
  setEvents(null);
}

function forceLogout() {
  closeEvents();
  audioEl.pause();
  audioEl.removeAttribute("src");
  audioEl.load();
  location.replace("/logout");
}

function connectEvents() {
  closeEvents();
  const roomID = currentRoomID;
  setEvents(new EventSource(`/rooms/${encodeURIComponent(roomID)}/events`));
  events.addEventListener("state", (event) => {
    if (roomID !== currentRoomID) {
      return;
    }
    try {
      renderState(JSON.parse(event.data));
    } catch (err) {
      recoverPlaybackClient("invalid playback state", err);
    }
  });
  events.addEventListener("disconnect", () => {
    if (roomID !== currentRoomID) {
      return;
    }
    forceLogout();
  });
  events.addEventListener("error", async () => {
    try {
      const info = await apiModule.api("/api/session");
      if (roomID === currentRoomID && info.disconnected?.[roomID]) {
        forceLogout();
      }
    } catch {
      // A network outage is not an administrative disconnect.
    }
  });
}

let renderState;

function setRenderState(fn) {
  renderState = fn;
}

export default {
  init,
  hasMedia,
  mediaURL,
  loadMedia,
  clearArtwork,
  loadArtwork,
  samePlaybackTimeline,
  mediaDuration,
  playbackPosition,
  setSyncedTime,
  playAudio,
  syncAudio,
  syncCurrentAudio,
  recoverPlaybackClient,
  closeEvents,
  forceLogout,
  connectEvents,
  setRenderState,
};
