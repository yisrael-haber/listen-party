const audio = document.getElementById("audio");
const trackEl = document.getElementById("track");
const artistEl = document.getElementById("artist");
const queueEl = document.getElementById("queue");
const historyEl = document.getElementById("history");
const resultsEl = document.getElementById("results");
const rescanButton = document.getElementById("rescan");
const rescanStatus = document.getElementById("rescanStatus");
const presenceEl = document.getElementById("presence");
const clearQueueButton = document.getElementById("clearQueue");
const togglePlaybackButton = document.getElementById("togglePlayback");
const seekInput = document.getElementById("seek");
const elapsedEl = document.getElementById("elapsed");
const durationEl = document.getElementById("duration");
const muteButton = document.getElementById("mute");
const volumeInput = document.getElementById("volume");
const searchInput = document.getElementById("q");
const searchStatus = document.getElementById("searchStatus");
const libraryStatus = document.getElementById("libraryStatus");
const volumeStorageKey = "listen-party-volume";
const mutedStorageKey = "listen-party-muted";
const syncToleranceSeconds = 0.1;

let currentID = 0;
let currentPlaybackID = 0;
let statusTimer = 0;
let lastState = null;
let lastStateReceivedAt = 0;
let searchTimer = 0;
let seeking = false;
let lastVolume = 1;

function label(track) {
  if (!track) return "";
  return [track.artist, track.album].filter(Boolean).join(" - ");
}

function trackSubtitle(track) {
  const parts = [track.artist, track.album].filter(Boolean);
  if (track.track_no) parts.push(`Track ${track.track_no}`);
  return parts.join(" - ");
}

function formatTime(seconds) {
  if (!Number.isFinite(seconds) || seconds < 0) seconds = 0;
  const total = Math.floor(seconds);
  const minutes = Math.floor(total / 60);
  const rest = String(total % 60).padStart(2, "0");
  return `${minutes}:${rest}`;
}

function mediaDuration() {
  return Number.isFinite(audio.duration) ? audio.duration : 0;
}

function setSeekUI(position) {
  const duration = mediaDuration();
  const max = Math.max(duration, position, 0);
  seekInput.max = String(Math.ceil(max));
  seekInput.disabled = !currentID;
  if (!seeking) {
    seekInput.value = String(Math.min(position, max));
  }
  elapsedEl.textContent = formatTime(seeking ? Number(seekInput.value) : position);
  durationEl.textContent = formatTime(duration);
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

function renderPlaybackButton(playing) {
  togglePlaybackButton.title = playing ? "Pause" : "Play";
  togglePlaybackButton.setAttribute("aria-label", playing ? "Pause" : "Play");
  togglePlaybackButton.firstElementChild.className = `playback-icon ${playing ? "pause-icon" : "play-icon"}`;
}

function renderVolumeButton() {
  const muted = audio.muted || audio.volume === 0;
  muteButton.title = muted ? "Unmute" : "Mute";
  muteButton.setAttribute("aria-label", muted ? "Unmute" : "Mute");
  muteButton.classList.toggle("muted", muted);
}

function loadLocalVolume() {
  const storedVolume = Number(sessionStorage.getItem(volumeStorageKey));
  if (Number.isFinite(storedVolume) && storedVolume >= 0 && storedVolume <= 1) {
    audio.volume = storedVolume;
  }
  lastVolume = audio.volume > 0 ? audio.volume : 1;
  audio.muted = sessionStorage.getItem(mutedStorageKey) === "true" || audio.volume === 0;
  volumeInput.value = String(audio.volume);
  renderVolumeButton();
}

function saveLocalVolume() {
  sessionStorage.setItem(volumeStorageKey, String(audio.volume));
  sessionStorage.setItem(mutedStorageKey, audio.muted ? "true" : "false");
}

function staleState(state) {
  if (lastState && state.revision < lastState.revision) {
    return true;
  }
  if (
    lastState &&
    state.revision === lastState.revision &&
    Date.parse(state.server_time) < Date.parse(lastState.server_time)
  ) {
    return true;
  }
  return false;
}

function renderState(state) {
  if (staleState(state)) {
    return;
  }

  lastState = state;
  lastStateReceivedAt = Date.now();

  const current = state.current;
  if (!current) {
    currentID = 0;
    currentPlaybackID = 0;
    audio.pause();
    audio.removeAttribute("src");
    setSeekUI(0);
    trackEl.textContent = "Nothing playing";
    artistEl.textContent = "";
  } else {
    trackEl.textContent = current.title;
    artistEl.textContent = label(current);
    if (currentPlaybackID !== state.playback_id) {
      currentID = current.id;
      currentPlaybackID = state.playback_id;
      audio.src = `/media/${current.id}`;
      audio.load();
    }
    syncAudio(state);
  }

  queueEl.replaceChildren(...state.queue.map(renderQueueItem));
  renderHistory(state.history || []);
  clearQueueButton.hidden = state.queue.length === 0;
  presenceEl.textContent = `${state.listener_count} listener${state.listener_count === 1 ? "" : "s"} connected`;
  togglePlaybackButton.disabled = !current && state.queue.length === 0;
  renderPlaybackButton(Boolean(current && !state.paused));
}

function renderQueueItem(item) {
  const li = document.createElement("li");
  li.className = "queue-item";

  const track = item.track;
  const meta = document.createElement("div");
  meta.className = "meta";
  meta.innerHTML = `<div class="title"></div><div class="sub"></div>`;
  meta.querySelector(".title").textContent = track ? track.title : `Track ${item.track_id}`;
  meta.querySelector(".sub").textContent = track ? trackSubtitle(track) : "";

  const actions = document.createElement("div");
  actions.className = "row-actions";
  actions.append(
    rowButton("Next", async () => {
      renderState(await api("/api/queue/next", {method: "POST", body: JSON.stringify({id: item.id})}));
    }),
    rowButton("Up", async () => {
      renderState(await api("/api/queue/move", {method: "POST", body: JSON.stringify({id: item.id, direction: -1})}));
    }),
    rowButton("Down", async () => {
      renderState(await api("/api/queue/move", {method: "POST", body: JSON.stringify({id: item.id, direction: 1})}));
    }),
    rowButton("Remove", async () => {
      renderState(await api("/api/queue/remove", {method: "POST", body: JSON.stringify({id: item.id})}));
    })
  );

  li.append(meta, actions);
  return li;
}

function renderHistoryItem(item) {
  const track = item.track;
  return trackRow(track || {id: item.track_id, title: `Track ${item.track_id}`}, [
    ["Play Now", async () => playNow(item.track_id)],
    ["Add", async () => addTrack(item.track_id)],
  ]);
}

function renderHistory(history) {
  if (history.length === 0) {
    const empty = document.createElement("p");
    empty.className = "hint";
    empty.textContent = "No tracks played yet";
    historyEl.replaceChildren(empty);
    return;
  }
  historyEl.replaceChildren(...history.map(renderHistoryItem));
}

function rowButton(text, action) {
  const button = document.createElement("button");
  button.className = "secondary compact";
  button.textContent = text;
  button.addEventListener("click", action);
  return button;
}

function trackRow(track, actions) {
  const row = document.createElement("div");
  row.className = "item";

  const meta = document.createElement("div");
  meta.className = "meta";
  meta.innerHTML = `<div class="title"></div><div class="sub"></div>`;
  meta.querySelector(".title").textContent = track.title;
  meta.querySelector(".sub").textContent = trackSubtitle(track);

  const actionEl = document.createElement("div");
  actionEl.className = "row-actions";
  actionEl.append(...actions.map(([text, action]) => rowButton(text, action)));

  row.append(meta, actionEl);
  return row;
}

async function addTrack(trackID) {
  renderState(await api("/api/queue", {method: "POST", body: JSON.stringify({track_id: trackID})}));
}

async function playNow(trackID) {
  renderState(await api("/api/playback/play-now", {method: "POST", body: JSON.stringify({track_id: trackID})}));
}

function setSyncedTime(target) {
  if (!Number.isFinite(target)) return;
  if (audio.readyState < HTMLMediaElement.HAVE_METADATA) return;
  if (Math.abs(audio.currentTime - target) > syncToleranceSeconds) {
    try {
      audio.currentTime = target;
    } catch (err) {
      console.warn("could not seek synchronized media yet", err);
    }
  }
}

function syncAudio(state) {
  if (!state.started_at) {
    setSeekUI(0);
    return;
  }
  const target = playbackPosition(state);
  setSeekUI(target);
  if (state.paused) {
    setSyncedTime(target);
    if (!audio.paused) {
      audio.pause();
    }
    return;
  }

  if (audio.paused) {
    audio.play().catch((err) => {
      console.warn("browser refused synchronized playback", err);
    });
  }
  setSyncedTime(target);
}

setInterval(() => {
  if (lastState && currentID) {
    syncAudio(lastState);
  }
}, 500);

async function publishPlayback(path, body = null) {
  try {
    const options = {method: "POST"};
    if (body) options.body = JSON.stringify(body);
    renderState(await api(path, options));
  } catch (err) {
    console.error(err);
  }
}

audio.addEventListener("loadedmetadata", () => {
  if (lastState && currentID) {
    syncAudio(lastState);
  } else {
    setSeekUI(0);
  }
});

audio.addEventListener("canplay", () => {
  if (lastState && currentID) {
    syncAudio(lastState);
  }
});

audio.addEventListener("ended", () => {
  if (!currentID) {
    return;
  }
  publishPlayback("/api/playback/ended", {track_id: currentID});
});

audio.addEventListener("timeupdate", () => {
  if (!seeking && currentID) {
    setSeekUI(audio.currentTime);
  }
});

async function api(path, options = {}) {
  const res = await fetch(path, {
    headers: {"Content-Type": "application/json"},
    ...options,
  });
  if (!res.ok) throw new Error(await res.text());
  if (res.status === 204) return null;
  return res.json();
}

async function loadLibraryStatus() {
  try {
    const info = await api("/api/library");
    libraryStatus.textContent = `${info.track_count} tracks indexed`;
  } catch (err) {
    libraryStatus.textContent = "Library status unavailable";
    console.error(err);
  }
}

async function runSearch() {
  const q = searchInput.value.trim();
  searchStatus.textContent = q ? "Searching..." : "Recently added";
  const tracks = await api(`/api/search?q=${encodeURIComponent(q)}`);
  searchStatus.textContent = q ? `${tracks.length} result${tracks.length === 1 ? "" : "s"}` : "Recently added";
  resultsEl.replaceChildren(...tracks.map((track) => trackRow(track, [
    ["Play Now", async () => playNow(track.id)],
    ["Add", async () => addTrack(track.id)],
  ])));
}

document.getElementById("searchForm").addEventListener("submit", async (event) => {
  event.preventDefault();
  await runSearch();
});

searchInput.addEventListener("input", () => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(() => {
    runSearch().catch(console.error);
  }, 180);
});

for (const [id, path] of [["skip", "/api/playback/skip"]]) {
  document.getElementById(id).addEventListener("click", async () => {
    renderState(await api(path, {method: "POST"}));
  });
}

togglePlaybackButton.addEventListener("click", async () => {
  if (lastState && lastState.current && !lastState.paused) {
    await publishPlayback("/api/playback/pause");
    return;
  }
  await publishPlayback("/api/playback/play");
});

seekInput.addEventListener("input", () => {
  seeking = true;
  setSeekUI(Number(seekInput.value));
});

seekInput.addEventListener("change", async () => {
  if (!currentID) {
    seeking = false;
    setSeekUI(0);
    return;
  }
  const positionMS = Math.max(0, Math.round(Number(seekInput.value) * 1000));
  seeking = false;
  await publishPlayback("/api/playback/seek", {position_ms: positionMS});
});

volumeInput.addEventListener("input", () => {
  audio.volume = Math.max(0, Math.min(1, Number(volumeInput.value)));
  audio.muted = audio.volume === 0;
  if (audio.volume > 0) {
    lastVolume = audio.volume;
  }
  saveLocalVolume();
  renderVolumeButton();
});

muteButton.addEventListener("click", () => {
  if (audio.muted || audio.volume === 0) {
    audio.muted = false;
    audio.volume = lastVolume > 0 ? lastVolume : 1;
    volumeInput.value = String(audio.volume);
  } else {
    lastVolume = audio.volume;
    audio.muted = true;
  }
  saveLocalVolume();
  renderVolumeButton();
});

renderPlaybackButton(false);
loadLocalVolume();

function setRescanStatus(message, kind = "") {
  clearTimeout(statusTimer);
  rescanStatus.textContent = message;
  rescanStatus.dataset.kind = kind;
  if (message && kind !== "working") {
    statusTimer = setTimeout(() => {
      rescanStatus.textContent = "";
      rescanStatus.dataset.kind = "";
    }, 4000);
  }
}

rescanButton.addEventListener("click", async () => {
  rescanButton.disabled = true;
  setRescanStatus("Rescanning library...", "working");
  try {
    await api("/api/admin/rescan", {method: "POST"});
    setRescanStatus("Library rescanned", "ok");
    await loadLibraryStatus();
    await runSearch();
  } catch (err) {
    setRescanStatus("Rescan failed", "error");
    console.error(err);
  } finally {
    rescanButton.disabled = false;
  }
});

clearQueueButton.addEventListener("click", async () => {
  renderState(await api("/api/queue/clear", {method: "POST"}));
});

new EventSource("/events").addEventListener("state", (event) => {
  renderState(JSON.parse(event.data));
});

loadLibraryStatus();
runSearch().catch(console.error);
api("/api/state").then(renderState).catch(console.error);
