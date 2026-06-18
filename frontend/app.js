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
const artworkEl = document.getElementById("artwork");
const seekInput = document.getElementById("seek");
const elapsedEl = document.getElementById("elapsed");
const durationEl = document.getElementById("duration");
const muteButton = document.getElementById("mute");
const volumeInput = document.getElementById("volume");
const searchInput = document.getElementById("q");
const searchField = document.getElementById("searchField");
const libraryStatus = document.getElementById("libraryStatus");
const currentUserEl = document.getElementById("currentUser");
const roomSelect = document.getElementById("roomSelect");
const logoutForm = document.getElementById("logoutForm");
const defaultVolume = 0.25;
const syncToleranceSeconds = 1;
const syncGuardMS = 1000;
const searchDebounceMS = 300;
const searchTextStorageKey = "listen-party.searchText";
const searchFieldStorageKey = "listen-party.searchField";

let lastState = null;
let lastStateReceivedAt = 0;
let searchTimer = 0;
let seeking = false;
let events = null;

let currentRoomID = decodeURIComponent(location.pathname.match(/^\/rooms\/([^/]+)/)?.[1] || "");

function roomAPI(path) {
  return `/rooms/${encodeURIComponent(currentRoomID)}${path}`;
}

function storageGet(key) {
  try {
    return localStorage.getItem(key) || "";
  } catch {
    return "";
  }
}

function storageSet(key, value) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // Persistence is optional; private browsing or storage policies may reject it.
  }
}

function restoreSearchPreferences() {
  searchInput.value = storageGet(searchTextStorageKey);
  const field = storageGet(searchFieldStorageKey);
  if ([...searchField.options].some((option) => option.value === field)) {
    searchField.value = field;
  }
}

function closeEvents() {
  events?.close();
  events = null;
}

function hasMedia() {
  return audio.hasAttribute("src");
}

function loadMedia(trackID) {
  const src = `/media/${trackID}`;
  if (audio.getAttribute("src") === src) {
    return;
  }
  audio.src = src;
  audio.load();
  loadArtwork(trackID);
}

function syncCurrentAudio() {
  if (lastState && hasMedia()) {
    syncAudio(lastState);
  }
}

function trackTitle(track) {
  if (!track) return "";
  return (track.title || `Track ${track.id || track.track_id || ""}`).trim();
}

function trackContext(track) {
  if (!track) return "";
  return [track.artist, track.album].filter(Boolean).join(" · ");
}

function trackSubtitle(track) {
  return [trackContext(track), track?.track_no ? `Track ${track.track_no}` : ""].filter(Boolean).join(" · ");
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
  const indexedMS = lastState?.current?.track?.duration_ms || 0;
  return indexedMS > 0 ? indexedMS / 1000 : 0;
}

function setSeekUI(position) {
  const duration = mediaDuration();
  const max = duration > 0 ? duration : Math.max(position, 0);
  const value = Math.min(position, max);
  seekInput.max = String(Math.ceil(max));
  seekInput.disabled = !hasMedia();
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
  const muted = audio.muted || audio.volume === 0;
  muteButton.title = muted ? "Unmute" : "Mute";
  muteButton.setAttribute("aria-label", muted ? "Unmute" : "Mute");
  muteButton.classList.toggle("muted", muted);
}

function setVolume(value) {
  const max = Number(volumeInput.max) || 1;
  audio.volume = Math.max(0, Math.min(max, value));
  audio.muted = audio.volume === 0;
  volumeInput.value = String(audio.volume);
}

function clearArtwork() {
  artworkEl.hidden = true;
  artworkEl.removeAttribute("src");
}

function loadArtwork(trackID) {
  artworkEl.hidden = true;
  artworkEl.src = `/media/${trackID}/artwork`;
}

artworkEl.addEventListener("load", () => {
  artworkEl.hidden = false;
});

artworkEl.addEventListener("error", clearArtwork);

function renderState(state) {
  if (state.room_id && currentRoomID && state.room_id !== currentRoomID) {
    return;
  }
  if (lastState && Date.parse(state.server_time) < Date.parse(lastState.server_time)) {
    return;
  }

  lastState = state;
  lastStateReceivedAt = Date.now();

  const queue = state.queue || [];
  const history = state.history || [];
  const current = state.current;
  const currentTrack = current?.track;
  if (!currentTrack) {
    audio.pause();
    audio.removeAttribute("src");
    audio.load();
    clearArtwork();
    setSeekUI(0);
    trackEl.textContent = "Nothing playing";
    artistEl.textContent = "";
  } else {
    trackEl.textContent = trackTitle(currentTrack);
    renderSubtitle(artistEl, trackContext(currentTrack), current.requested_by);
    loadMedia(currentTrack.id);
    syncAudio(state);
  }

  queueEl.replaceChildren(...queue.map(renderQueueItem));
  renderHistory(history);
  clearQueueButton.hidden = queue.length === 0;
  clearHistoryButton.hidden = history.length === 0;
  renderPresence(state);
  previousButton.disabled = history.length === 0;
  togglePlaybackButton.disabled = !currentTrack && queue.length === 0;
  renderPlaybackButton(Boolean(currentTrack && !state.paused));
}

function renderPresence(state) {
  const listeners = Array.isArray(state.listeners) ? state.listeners : [];
  const count = listeners.length;
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
    track ? trackTitle(track) : `Track ${item.track_id}`,
    track ? trackSubtitle(track) : "",
    item.requested_by
  );

  const actions = document.createElement("div");
  actions.className = "row-actions";
  actions.append(
    commandButton("Next", {action: "queue_next", id: item.id}),
    commandButton("Up", {action: "queue_move", id: item.id, direction: -1}),
    commandButton("Down", {action: "queue_move", id: item.id, direction: 1}),
    commandButton("Remove", {action: "queue_remove", id: item.id})
  );

  li.append(meta, actions);
  return li;
}

function renderHistoryItem(item) {
  const track = item.track;
  const trackID = item.track_id;
  return trackRow(track || {id: item.track_id, title: `Track ${item.track_id}`}, [
    ["Queue", {action: "queue_add", track_id: trackID}],
    ["Play", {action: "play_now", track_id: trackID}],
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

function commandButton(text, body) {
  const button = document.createElement("button");
  button.className = "secondary compact";
  button.textContent = text;
  button.addEventListener("click", async () => {
    await command(body);
  });
  return button;
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

  const meta = trackMeta(trackTitle(track), trackSubtitle(track), requestedBy);

  const actionEl = document.createElement("div");
  actionEl.className = "row-actions";
  actionEl.append(...actions.map(([text, body]) => commandButton(text, body)));

  row.append(meta, actionEl);
  return row;
}

function renderSubtitle(element, subtitleText, requestedBy = "") {
  element.replaceChildren();
  if (subtitleText) {
    const context = document.createElement("span");
    context.className = "track-context";
    context.textContent = subtitleText;
    element.append(context);
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

async function command(body) {
  const state = await api(roomAPI("/api/command"), {
    method: "POST",
    body: JSON.stringify(body),
  });
  renderState(state);
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

function playAudio() {
  if (!hasMedia()) {
    return;
  }
  audio.play().catch((err) => {
    console.warn("browser refused synchronized playback", err);
  });
}

function syncAudio(state) {
  if (!state.started_at) {
    setSeekUI(0);
    return;
  }
  const target = playbackPosition(state);
  const duration = mediaDuration();
  if (!state.paused && duration > 0 && target > duration) {
    setSeekUI(duration);
    return;
  }
  if (state.paused) {
    setSeekUI(target);
    setSyncedTime(target);
    if (!audio.paused) {
      audio.pause();
    }
    return;
  }

  setSyncedTime(target);
  if (audio.paused) playAudio();
  setSeekUI(audio.readyState >= HTMLMediaElement.HAVE_METADATA ? audio.currentTime : target);
}

setInterval(syncCurrentAudio, syncGuardMS);

for (const eventName of ["loadedmetadata", "canplay"]) {
  audio.addEventListener(eventName, syncCurrentAudio);
}

audio.addEventListener("timeupdate", () => {
  if (!seeking && hasMedia()) {
    setSeekUI(audio.currentTime);
  }
});

audio.addEventListener("volumechange", renderVolumeButton);

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

async function loadRooms() {
  const info = await api("/api/session");
  currentUserEl.textContent = info.user?.username || "Signed in";
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
  const field = searchField.value;
  const params = new URLSearchParams({q, field});
  const tracks = await api(`/api/search?${params}`);
  if (q !== searchInput.value.trim() || field !== searchField.value) {
    return;
  }
  resultsEl.replaceChildren(...tracks.map((track) => trackRow(track, [
    ["Queue", {action: "queue_add", track_id: track.id}],
    ["Play", {action: "play_now", track_id: track.id}],
  ])));
}

document.getElementById("searchForm").addEventListener("submit", async (event) => {
  event.preventDefault();
  await runSearch();
});

searchInput.addEventListener("input", () => {
  storageSet(searchTextStorageKey, searchInput.value);
  clearTimeout(searchTimer);
  resultsEl.replaceChildren();
  searchTimer = setTimeout(() => {
    runSearch().catch(console.error);
  }, searchDebounceMS);
});

searchField.addEventListener("change", () => {
  storageSet(searchFieldStorageKey, searchField.value);
  clearTimeout(searchTimer);
  runSearch().catch(console.error);
});

for (const [id, action] of [["previous", "previous"], ["skip", "skip"]]) {
  document.getElementById(id).addEventListener("click", async () => {
    await command({action});
  });
}

togglePlaybackButton.addEventListener("click", async () => {
  if (lastState && lastState.current && !lastState.paused) {
    await command({action: "pause"});
    return;
  }
  await command({action: "play"});
});

seekInput.addEventListener("input", () => {
  seeking = true;
  setSeekUI(Number(seekInput.value));
});

seekInput.addEventListener("change", async () => {
  if (!hasMedia()) {
    seeking = false;
    setSeekUI(0);
    return;
  }
  const positionMS = Math.max(0, Math.round(Number(seekInput.value) * 1000));
  seeking = false;
  await command({action: "seek", position_ms: positionMS});
  syncCurrentAudio();
});

volumeInput.addEventListener("input", () => {
  const next = Number(volumeInput.value);
  if (!Number.isFinite(next)) return;
  setVolume(next);
  syncCurrentAudio();
});

muteButton.addEventListener("click", () => {
  if (audio.muted || audio.volume === 0) {
    if (audio.volume === 0) {
      setVolume(defaultVolume);
    }
    audio.muted = false;
    syncCurrentAudio();
    return;
  }
  audio.muted = true;
  renderVolumeButton();
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

restoreSearchPreferences();
renderPlaybackButton(false);
setVolume(0);
renderVolumeButton();

clearQueueButton.addEventListener("click", async () => {
  await command({action: "queue_clear"});
});

clearHistoryButton.addEventListener("click", async () => {
  await command({action: "history_clear"});
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

async function start() {
  if (!await loadRooms()) {
    return;
  }
  closeEvents();
  events = new EventSource(`/rooms/${encodeURIComponent(currentRoomID)}/events`);
  events.addEventListener("state", (event) => {
    renderState(JSON.parse(event.data));
  });
  loadLibraryStatus();
  runSearch().catch(console.error);
  api(roomAPI("/api/state")).then(renderState).catch(console.error);
}

start().catch(console.error);
