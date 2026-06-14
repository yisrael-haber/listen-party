const audio = document.getElementById("audio");
const trackEl = document.getElementById("track");
const artistEl = document.getElementById("artist");
const queueEl = document.getElementById("queue");
const historyEl = document.getElementById("history");
const resultsEl = document.getElementById("results");
const presenceEl = document.getElementById("presence");
const presenceButton = document.getElementById("presenceButton");
const listenerListEl = document.getElementById("listenerList");
const clearQueueButton = document.getElementById("clearQueue");
const clearHistoryButton = document.getElementById("clearHistory");
const previousButton = document.getElementById("previous");
const togglePlaybackButton = document.getElementById("togglePlayback");
const seekInput = document.getElementById("seek");
const elapsedEl = document.getElementById("elapsed");
const durationEl = document.getElementById("duration");
const muteButton = document.getElementById("mute");
const volumeInput = document.getElementById("volume");
const searchInput = document.getElementById("q");
const libraryStatus = document.getElementById("libraryStatus");
const currentUserEl = document.getElementById("currentUser");
const roomSelect = document.getElementById("roomSelect");
const logoutForm = document.getElementById("logoutForm");
const volumeStorageKey = "listen-party-volume";
const mutedStorageKey = "listen-party-muted";
const syncToleranceSeconds = 0.1;
const searchDebounceMS = 300;

let currentID = 0;
let currentPlaybackID = 0;
let lastState = null;
let lastStateReceivedAt = 0;
let searchTimer = 0;
let seeking = false;
let lastVolume = 1;
let localVolume = 1;
let localMuted = false;
let audioContext = null;
let gainNode = null;
let mediaSource = null;
let events = null;
let endedPostPlaybackID = 0;

function currentRoomIDFromPath() {
  const match = location.pathname.match(/^\/rooms\/([^/]+)/);
  return match ? decodeURIComponent(match[1]) : "";
}

let currentRoomID = currentRoomIDFromPath();

function roomAPI(path) {
  return `/rooms/${encodeURIComponent(currentRoomID)}${path}`;
}

function closeEvents() {
  if (!events) {
    return;
  }
  events.close();
  events = null;
}

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
  if (Number.isFinite(audio.duration) && audio.duration > 0) {
    return audio.duration;
  }
  const indexedMS = lastState?.current?.duration_ms || 0;
  return indexedMS > 0 ? indexedMS / 1000 : 0;
}

function setSeekUI(position) {
  const duration = mediaDuration();
  const max = duration > 0 ? duration : Math.max(position, 0);
  const value = Math.min(position, max);
  seekInput.max = String(Math.ceil(max));
  seekInput.disabled = !currentID;
  if (!seeking) {
    seekInput.value = String(value);
  }
  elapsedEl.textContent = formatTime(seeking ? Number(seekInput.value) : value);
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
  const muted = localMuted || localVolume === 0;
  muteButton.title = muted ? "Unmute" : "Mute";
  muteButton.setAttribute("aria-label", muted ? "Unmute" : "Mute");
  muteButton.classList.toggle("muted", muted);
}

function loadLocalVolume() {
  const storedVolume = Number(sessionStorage.getItem(volumeStorageKey));
  if (Number.isFinite(storedVolume) && storedVolume >= 0 && storedVolume <= 1) {
    localVolume = storedVolume;
  }
  localMuted = sessionStorage.getItem(mutedStorageKey) === "true" || localVolume === 0;
  lastVolume = localVolume > 0 ? localVolume : 1;
  volumeInput.value = String(localVolume);
  audio.muted = false;
  applyLocalVolume();
}

function saveLocalVolume() {
  sessionStorage.setItem(volumeStorageKey, String(localVolume));
  sessionStorage.setItem(mutedStorageKey, localMuted ? "true" : "false");
}

function ensureAudioGraph() {
  if (gainNode) {
    if (audioContext.state === "suspended") {
      audioContext.resume().catch(console.error);
    }
    return;
  }
  const AudioContext = window.AudioContext || window.webkitAudioContext;
  if (!AudioContext) {
    return;
  }
  audioContext = new AudioContext();
  mediaSource = audioContext.createMediaElementSource(audio);
  gainNode = audioContext.createGain();
  mediaSource.connect(gainNode);
  gainNode.connect(audioContext.destination);
  audio.volume = 1;
  audio.muted = false;
  audioContext.resume().catch(console.error);
}

function applyLocalVolume() {
  const gain = localMuted ? 0 : localVolume;
  if (gainNode) {
    gainNode.gain.setTargetAtTime(gain, audioContext.currentTime, 0.01);
  } else {
    audio.volume = gain;
    audio.muted = false;
  }
  renderVolumeButton();
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
    renderSubtitle(artistEl, label(current), state.current_requested_by);
    if (currentPlaybackID !== state.playback_id) {
      currentID = current.id;
      currentPlaybackID = state.playback_id;
      endedPostPlaybackID = 0;
      audio.src = `/media/${current.id}`;
      audio.load();
    }
    syncAudio(state);
  }

  queueEl.replaceChildren(...state.queue.map(renderQueueItem));
  renderHistory(state.history || []);
  clearQueueButton.hidden = state.queue.length === 0;
  clearHistoryButton.hidden = !state.history || state.history.length === 0;
  renderPresence(state);
  previousButton.disabled = !state.history || state.history.length === 0;
  togglePlaybackButton.disabled = !current && state.queue.length === 0;
  renderPlaybackButton(Boolean(current && !state.paused));
}

function renderPresence(state) {
  const listeners = Array.isArray(state.listeners) ? state.listeners : [];
  const count = Number.isFinite(state.listener_count) ? state.listener_count : listeners.length;
  presenceEl.textContent = `${count} listener${count === 1 ? "" : "s"}`;
  listenerListEl.replaceChildren(...listeners.map((username) => {
    const item = document.createElement("div");
    item.className = "listener-item";
    item.textContent = username;
    return item;
  }));
  if (listeners.length === 0) {
    const empty = document.createElement("div");
    empty.className = "listener-item empty";
    empty.textContent = "No active users";
    listenerListEl.append(empty);
  }
}

function renderQueueItem(item) {
  const li = document.createElement("li");
  li.className = "queue-item";

  const track = item.track;
  const meta = trackMeta(
    track ? track.title : `Track ${item.track_id}`,
    track ? trackSubtitle(track) : "",
    item.requested_by
  );

  const actions = document.createElement("div");
  actions.className = "row-actions";
  actions.append(
    stateButton("Next", "/api/queue/next", {id: item.id}),
    stateButton("Up", "/api/queue/move", {id: item.id, direction: -1}),
    stateButton("Down", "/api/queue/move", {id: item.id, direction: 1}),
    stateButton("Remove", "/api/queue/remove", {id: item.id})
  );

  li.append(meta, actions);
  return li;
}

function renderHistoryItem(item) {
  const track = item.track;
  return trackRow(track || {id: item.track_id, title: `Track ${item.track_id}`}, [
    ["Queue", async () => addTrack(item.track_id)],
    ["Play", async () => playNow(item.track_id)],
  ], item.requested_by);
}

function renderHistory(history) {
  if (history.length === 0) {
    const empty = document.createElement("p");
    empty.className = "hint";
    empty.textContent = "No previously played tracks";
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

function stateButton(text, path, body = null) {
  return rowButton(text, async () => {
    await postState(roomAPI(path), body);
  });
}

function trackMeta(titleText, subtitleText, requestedBy = "") {
  const meta = document.createElement("div");
  meta.className = "meta";

  const title = document.createElement("div");
  title.className = "title";
  title.textContent = titleText;

  const sub = document.createElement("div");
  sub.className = "sub";
  renderSubtitle(sub, subtitleText, requestedBy);

  meta.append(title, sub);
  return meta;
}

function trackRow(track, actions, requestedBy = "") {
  const row = document.createElement("div");
  row.className = "item";

  const meta = trackMeta(track.title, trackSubtitle(track), requestedBy);

  const actionEl = document.createElement("div");
  actionEl.className = "row-actions";
  actionEl.append(...actions.map(([text, action]) => rowButton(text, action)));

  row.append(meta, actionEl);
  return row;
}

function renderSubtitle(element, subtitleText, requestedBy = "") {
  element.replaceChildren();
  if (subtitleText) {
    element.append(document.createTextNode(subtitleText));
  }
  if (!requestedBy) {
    return;
  }
  if (subtitleText) {
    element.append(document.createTextNode(" - Queued by "));
  } else {
    element.append(document.createTextNode("Queued by "));
  }
  const requester = document.createElement("span");
  requester.className = "requester";
  requester.textContent = requestedBy;
  element.append(requester);
}

async function addTrack(trackID) {
  await postState(roomAPI("/api/queue"), {track_id: trackID});
}

async function playNow(trackID) {
  await postState(roomAPI("/api/playback/play-now"), {track_id: trackID});
}

async function postState(path, body = null) {
  const options = {method: "POST"};
  if (body) options.body = JSON.stringify(body);
  renderState(await api(path, options));
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
  const duration = mediaDuration();
  if (!state.paused && duration > 0 && target > duration + 1.5 && currentID && endedPostPlaybackID !== currentPlaybackID) {
    endedPostPlaybackID = currentPlaybackID;
    postState(roomAPI("/api/playback/ended"), {track_id: currentID}).catch(console.error);
    setSeekUI(duration);
    return;
  }
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
  postState(roomAPI("/api/playback/ended"), {track_id: currentID}).catch(console.error);
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

async function loadCurrentUser() {
  try {
    const user = await api("/api/me");
    currentUserEl.textContent = user.username || "Signed in";
  } catch (err) {
    currentUserEl.textContent = "Signed in";
    console.error(err);
  }
}

async function loadRooms() {
  const info = await api("/api/rooms");
  const rooms = info.rooms || [];
  if (!currentRoomID) {
    currentRoomID = info.default_room_id || (rooms[0] && rooms[0].id) || "public";
  }
  if (rooms.length > 0 && !rooms.some((room) => room.id === currentRoomID)) {
    location.href = `/rooms/${encodeURIComponent(rooms[0].id)}`;
    return false;
  }
  roomSelect.replaceChildren(...rooms.map((room) => {
    const option = document.createElement("option");
    option.value = room.id;
    option.textContent = room.name || room.id;
    return option;
  }));
  roomSelect.value = currentRoomID;
  roomSelect.disabled = rooms.length <= 1;
  return true;
}

async function runSearch() {
  const q = searchInput.value.trim();
  const tracks = await api(`/api/search?q=${encodeURIComponent(q)}`);
  if (q !== searchInput.value.trim()) {
    return;
  }
  resultsEl.replaceChildren(...tracks.map((track) => trackRow(track, [
    ["Queue", async () => addTrack(track.id)],
    ["Play", async () => playNow(track.id)],
  ])));
}

document.getElementById("searchForm").addEventListener("submit", async (event) => {
  event.preventDefault();
  await runSearch();
});

searchInput.addEventListener("input", () => {
  clearTimeout(searchTimer);
  resultsEl.replaceChildren();
  searchTimer = setTimeout(() => {
    runSearch().catch(console.error);
  }, searchDebounceMS);
});

for (const [id, path] of [["previous", "/api/playback/previous"], ["skip", "/api/playback/skip"]]) {
  document.getElementById(id).addEventListener("click", async () => {
    await postState(roomAPI(path));
  });
}

togglePlaybackButton.addEventListener("click", async () => {
  if (lastState && lastState.current && !lastState.paused) {
    await postState(roomAPI("/api/playback/pause"));
    return;
  }
  await postState(roomAPI("/api/playback/play"));
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
  await postState(roomAPI("/api/playback/seek"), {position_ms: positionMS});
});

volumeInput.addEventListener("input", () => {
  ensureAudioGraph();
  const next = Number(volumeInput.value);
  if (!Number.isFinite(next)) return;
  localVolume = Math.max(0, Math.min(1, next));
  localMuted = localVolume === 0;
  if (localVolume > 0) {
    lastVolume = localVolume;
  }
  applyLocalVolume();
});

volumeInput.addEventListener("change", () => {
  saveLocalVolume();
});

muteButton.addEventListener("click", () => {
  ensureAudioGraph();
  if (localMuted || localVolume === 0) {
    localMuted = false;
    localVolume = lastVolume > 0 ? lastVolume : 1;
    volumeInput.value = String(localVolume);
  } else {
    lastVolume = localVolume;
    localMuted = true;
  }
  applyLocalVolume();
  saveLocalVolume();
});

presenceButton.addEventListener("click", () => {
  const nextOpen = listenerListEl.hidden;
  listenerListEl.hidden = !nextOpen;
  presenceButton.setAttribute("aria-expanded", String(nextOpen));
});

document.addEventListener("click", (event) => {
  if (event.target.closest(".presence-menu")) {
    return;
  }
  listenerListEl.hidden = true;
  presenceButton.setAttribute("aria-expanded", "false");
});

document.addEventListener("keydown", (event) => {
  if (event.key !== "Escape") {
    return;
  }
  listenerListEl.hidden = true;
  presenceButton.setAttribute("aria-expanded", "false");
});

renderPlaybackButton(false);
loadLocalVolume();

clearQueueButton.addEventListener("click", async () => {
  await postState(roomAPI("/api/queue/clear"));
});

clearHistoryButton.addEventListener("click", async () => {
  await postState(roomAPI("/api/history/clear"));
});

roomSelect.addEventListener("change", () => {
  if (!roomSelect.value || roomSelect.value === currentRoomID) {
    return;
  }
  closeEvents();
  roomSelect.disabled = true;
  location.href = `/rooms/${encodeURIComponent(roomSelect.value)}`;
});

logoutForm.addEventListener("submit", () => {
  closeEvents();
});

window.addEventListener("pagehide", closeEvents);
window.addEventListener("beforeunload", closeEvents);

async function start() {
  if (!await loadRooms()) {
    return;
  }
  closeEvents();
  events = new EventSource(`/rooms/${encodeURIComponent(currentRoomID)}/events`);
  events.addEventListener("state", (event) => {
    renderState(JSON.parse(event.data));
  });
  events.addEventListener("error", () => {
    if (document.visibilityState === "hidden") {
      closeEvents();
    }
  });
  loadLibraryStatus();
  loadCurrentUser();
  runSearch().catch(console.error);
  api(roomAPI("/api/state")).then(renderState).catch(console.error);
}

start().catch(console.error);
