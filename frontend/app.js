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
const skipButton = document.getElementById("skip");
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
const libraryTab = document.getElementById("libraryTab");
const playlistsTab = document.getElementById("playlistsTab");
const libraryViews = document.querySelectorAll(".library-view");
const playlistsView = document.getElementById("playlistsView");
const playlistSelect = document.getElementById("playlistSelect");
const queuePlaylistButton = document.getElementById("queuePlaylist");
const shufflePlaylistButton = document.getElementById("shufflePlaylist");
const deletePlaylistButton = document.getElementById("deletePlaylist");
const playlistDetailEl = document.getElementById("playlistDetail");
const newPlaylistButton = document.getElementById("newPlaylist");
const playlistCreatePanel = document.getElementById("playlistCreatePanel");
const playlistCreateForm = document.getElementById("playlistCreateForm");
const playlistNameInput = document.getElementById("playlistName");
const currentUserEl = document.getElementById("currentUser");
const roomSelect = document.getElementById("roomSelect");
const logoutForm = document.getElementById("logoutForm");
const defaultVolume = 0.25;
const syncToleranceSeconds = 1;
const syncGuardMS = 1000;
const searchDebounceMS = 300;
const searchTextStorageKey = "listen-party.searchText";
const searchFieldStorageKey = "listen-party.searchField";
const railModeStorageKey = "listen-party.railMode";
const playlistStorageKey = "listen-party.selectedPlaylist";

let lastState = null;
let lastStateReceivedAt = 0;
let searchTimer = 0;
let seeking = false;
let events = null;
let playlists = [];
let selectedPlaylistID = 0;
let currentPermissions = new Set();
let queueSortable = null;
let queueDragActive = false;
let queueReorderPending = false;
let pendingQueueState = null;

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

function restoreRailPreferences() {
	const storedPlaylistID = Number(storageGet(playlistStorageKey));
	selectedPlaylistID = Number.isInteger(storedPlaylistID) && storedPlaylistID > 0 ? storedPlaylistID : 0;
	const mode = storageGet(railModeStorageKey) === "playlists" ? "playlists" : "library";
	setRailMode(mode, {load: false, persist: false});
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
  return (track.title || `Track ${track.id || ""}`).trim();
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
  seekInput.disabled = !hasMedia() || !hasRoomPermission("playback_control");
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
  if (queueDragActive || queueReorderPending) {
    if (!pendingQueueState || Date.parse(state.server_time) >= Date.parse(pendingQueueState.server_time)) {
      pendingQueueState = state;
    }
    return;
  }
  if (Array.isArray(state.permissions)) {
    currentPermissions = new Set(state.permissions);
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
  const canManageQueue = hasRoomPermission("queue_manage");
  const canControlPlayback = hasRoomPermission("playback_control");
  clearQueueButton.hidden = !canManageQueue || queue.length === 0;
  clearHistoryButton.hidden = !canManageQueue || history.length === 0;
  renderPresence(state);
  previousButton.disabled = !canControlPlayback || history.length === 0;
  skipButton.disabled = !canControlPlayback;
  togglePlaybackButton.disabled = !canControlPlayback || (!currentTrack && queue.length === 0);
  refreshPermissionControls();
  updateQueueSortable();
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
  li.dataset.queueItemId = String(item.id);

  const track = item.track;
  const meta = trackMeta(
    track ? trackTitle(track) : "Unavailable track",
    track ? trackSubtitle(track) : "",
    item.requested_by
  );

  const actions = trackActionGroup([], item.dedupe_key, [
    commandTrashButton("Remove from queue", {action: "queue_remove", queue_item_id: item.id}),
  ]);

	if (hasRoomPermission("queue_manage")) {
		li.classList.add("queue-item-draggable");
		li.append(queueDragHandle(item), meta, actions);
	} else {
		li.append(meta, actions);
	}
  return li;
}

function queueDragHandle(item) {
	const handle = document.createElement("button");
	handle.className = "queue-drag-handle";
	handle.type = "button";
	handle.title = "Drag to reorder";
	handle.setAttribute("aria-label", `Reorder ${item.track ? trackTitle(item.track) : "unavailable track"}`);
	const icon = document.createElement("span");
	icon.className = "queue-drag-icon";
	icon.setAttribute("aria-hidden", "true");
	handle.append(icon);
	handle.addEventListener("keydown", (event) => {
		handleQueueReorderKey(event, item.id);
	});
	return handle;
}

function handleQueueReorderKey(event, queueItemID) {
	if (!["ArrowUp", "ArrowDown", "Home", "End"].includes(event.key) || queueReorderPending) return;
	const item = event.currentTarget.closest(".queue-item");
	if (!item) return;
	let before = null;
	if (event.key === "ArrowUp") {
		before = item.previousElementSibling;
		if (!before) return;
	} else if (event.key === "ArrowDown") {
		const next = item.nextElementSibling;
		if (!next) return;
		before = next.nextElementSibling;
	} else if (event.key === "Home") {
		before = queueEl.firstElementChild;
		if (before === item) return;
	}
	event.preventDefault();
	const beforeQueueItemID = before ? Number(before.dataset.queueItemId) : 0;
	submitQueueReorder(queueItemID, beforeQueueItemID).then(() => {
		queueEl.querySelector(`[data-queue-item-id="${queueItemID}"] .queue-drag-handle`)?.focus();
	});
}

function renderHistoryItem(item) {
	const track = item.track;
	const dedupeKey = item.dedupe_key;
	return trackRow(track || {title: "Unavailable track", dedupe_key: dedupeKey}, standardTrackCommands(dedupeKey), item.requested_by, dedupeKey);
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
  button.dataset.roomAction = body.action;
  button.hidden = !canRunCommand(body.action);
  button.addEventListener("click", async () => {
    await command(body);
  });
  return button;
}

function commandTrashButton(label, body) {
	const button = trashButton(label, async () => {
		await command(body);
	});
	button.dataset.roomAction = body.action;
	button.hidden = !canRunCommand(body.action);
	return button;
}

function trashButton(label, onClick) {
	const button = document.createElement("button");
	button.className = "secondary compact icon-only trash-button";
	button.type = "button";
	button.title = label;
	button.setAttribute("aria-label", label);
	button.append(document.createElement("span"));
	button.addEventListener("click", onClick);
	return button;
}

function refreshPermissionControls() {
  document.querySelectorAll("[data-room-action]").forEach((button) => {
    button.hidden = !canRunCommand(button.dataset.roomAction);
  });
  document.querySelectorAll(".item .row-actions, .queue-item .row-actions").forEach(updateRowActionLayout);
  updatePlaylistActionButtons();
}

function updateRowActionLayout(actions) {
  const visibleRoomActions = [...actions.querySelectorAll("[data-room-action]")].filter((button) => !button.hidden);
  const hasRoomActions = visibleRoomActions.length > 0;
  const hasPlaylistAction = Boolean(actions.querySelector(".playlist-more-button"));
  const hasStandaloneAction = [...actions.children].some((element) => element.matches("button:not([data-room-action])") && !element.hidden);
  actions.classList.toggle("playlist-only", !hasRoomActions && hasPlaylistAction);
  actions.classList.toggle("no-actions", !hasRoomActions && !hasPlaylistAction && !hasStandaloneAction);
  actions.classList.toggle("single-room-action", visibleRoomActions.length === 1);
  actions.classList.toggle("has-standalone-action", hasStandaloneAction);
  actions.classList.toggle("standalone-only", !hasRoomActions && !hasPlaylistAction && hasStandaloneAction);
}

function hasRoomPermission(permission) {
  return currentPermissions.has(permission);
}

function canRunCommand(action) {
  if (["play", "play_now", "pause", "previous", "seek", "skip"].includes(action)) {
    return hasRoomPermission("playback_control");
  }
  if (["queue_add", "playlist_queue", "playlist_shuffle"].includes(action)) {
    return hasRoomPermission("queue_add");
  }
  return hasRoomPermission("queue_manage");
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

function standardTrackCommands(dedupeKey) {
	if (!dedupeKey) return [];
	return [
		["Queue", {action: "queue_add", dedupe_key: dedupeKey}],
		["Play", {action: "play_now", dedupe_key: dedupeKey}],
	];
}

function trackActionGroup(commandSpecs, dedupeKey, extraButtons = []) {
	const actions = document.createElement("div");
	actions.className = "row-actions";
	actions.append(...commandSpecs.map(([text, body]) => commandButton(text, body)));
	if (dedupeKey) {
		actions.append(addToPlaylistButton(dedupeKey));
	}
	actions.append(...extraButtons);
	updateRowActionLayout(actions);
	return actions;
}

function trackRow(track, commandSpecs, requestedBy = "", dedupeKey = track?.dedupe_key || "", extraButtons = []) {
	const row = document.createElement("div");
	row.className = "item";

	const meta = trackMeta(trackTitle(track), trackSubtitle(track), requestedBy);
	const actionEl = trackActionGroup(commandSpecs, dedupeKey, extraButtons);

	row.append(meta, actionEl);
	return row;
}

function addToPlaylistButton(dedupeKey) {
	const editable = playlists.filter((playlist) => playlist.can_edit);
	if (editable.length === 0) {
		const button = document.createElement("button");
		button.className = "secondary compact playlist-more-button";
		button.type = "button";
		setPlaylistButtonContent(button);
		button.setAttribute("aria-label", "Add to playlist");
		button.disabled = true;
		return button;
	}
	const wrap = document.createElement("div");
	wrap.className = "playlist-add-menu";
	const button = document.createElement("button");
	button.className = "secondary compact playlist-more-button";
	button.type = "button";
	setPlaylistButtonContent(button);
	button.setAttribute("aria-label", "Add to playlist");
	button.setAttribute("aria-haspopup", "menu");
	button.setAttribute("aria-expanded", "false");
	const menu = document.createElement("div");
	menu.className = "playlist-add-options";
	menu.hidden = true;
	for (const playlist of editable) {
		const item = document.createElement("button");
		item.type = "button";
		item.className = "playlist-add-option";
		item.textContent = playlist.name;
		item.addEventListener("click", async () => {
			menu.hidden = true;
			button.setAttribute("aria-expanded", "false");
			await api(`/api/playlists/${playlist.id}/items`, {
				method: "POST",
				body: JSON.stringify({dedupe_key: dedupeKey}),
			});
			await loadPlaylists(playlist.id);
		});
		menu.append(item);
	}
	button.addEventListener("click", (event) => {
		event.stopPropagation();
		closePlaylistAddMenus(wrap);
		const open = menu.hidden;
		menu.hidden = !open;
		button.setAttribute("aria-expanded", String(open));
	});
	wrap.append(button, menu);
	return wrap;
}

function setPlaylistButtonContent(button) {
	const icon = document.createElement("span");
	icon.className = "playlist-add-icon";
	icon.textContent = "+";
	const label = document.createElement("span");
	label.className = "playlist-add-label";
	label.textContent = "Playlist";
	button.replaceChildren(icon, label);
}

function closePlaylistAddMenus(except = null) {
	document.querySelectorAll(".playlist-add-menu").forEach((wrap) => {
		if (wrap === except) return;
		const menu = wrap.querySelector(".playlist-add-options");
		const button = wrap.querySelector("button");
		if (menu) menu.hidden = true;
		if (button) button.setAttribute("aria-expanded", "false");
	});
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

function initQueueSortable() {
	if (typeof Sortable === "undefined") {
		throw new Error("embedded SortableJS asset did not load");
	}
	const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
	queueSortable = Sortable.create(queueEl, {
		animation: reduceMotion ? 0 : 160,
		easing: "cubic-bezier(0.2, 0, 0, 1)",
		handle: ".queue-drag-handle",
		draggable: ".queue-item",
		dataIdAttr: "data-queue-item-id",
		ghostClass: "queue-sortable-ghost",
		chosenClass: "queue-sortable-chosen",
		dragClass: "queue-sortable-drag",
		forceFallback: true,
		fallbackOnBody: true,
		fallbackTolerance: 4,
		delay: 120,
		delayOnTouchOnly: true,
		touchStartThreshold: 4,
		onStart() {
			queueDragActive = true;
			pendingQueueState = null;
			queueEl.classList.add("queue-dragging");
		},
		onEnd(event) {
			queueDragActive = false;
			queueEl.classList.remove("queue-dragging");
			if (event.oldDraggableIndex === event.newDraggableIndex) {
				applyPendingQueueState();
				return;
			}
			const queueItemID = Number(event.item.dataset.queueItemId);
			const before = event.item.nextElementSibling;
			const beforeQueueItemID = before ? Number(before.dataset.queueItemId) : 0;
			submitQueueReorder(queueItemID, beforeQueueItemID);
		},
	});
	updateQueueSortable();
}

function updateQueueSortable() {
	if (!queueSortable) return;
	const enabled = hasRoomPermission("queue_manage") && !queueReorderPending;
	queueSortable.option("disabled", !enabled);
	queueEl.classList.toggle("queue-sortable-enabled", enabled);
}

function applyPendingQueueState() {
	const state = pendingQueueState;
	pendingQueueState = null;
	if (state) renderState(state);
}

async function submitQueueReorder(queueItemID, beforeQueueItemID) {
	if (queueReorderPending || !hasRoomPermission("queue_manage")) return;
	queueReorderPending = true;
	updateQueueSortable();
	try {
		const state = await api(roomAPI("/api/command"), {
			method: "POST",
			body: JSON.stringify({
				action: "queue_reorder",
				queue_item_id: queueItemID,
				before_queue_item_id: beforeQueueItemID,
			}),
		});
		queueReorderPending = false;
		renderState(state);
		applyPendingQueueState();
	} catch (err) {
		console.error(err);
		queueReorderPending = false;
		pendingQueueState = null;
		try {
			renderState(await api(roomAPI("/api/state")));
		} catch (refreshErr) {
			console.error(refreshErr);
		}
		queueEl.classList.add("queue-reorder-error");
		setTimeout(() => queueEl.classList.remove("queue-reorder-error"), 500);
	} finally {
		updateQueueSortable();
	}
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

function setRailMode(mode, {load = true, persist = true} = {}) {
	const playlistsActive = mode === "playlists";
	if (persist) {
		storageSet(railModeStorageKey, playlistsActive ? "playlists" : "library");
	}
	libraryTab.classList.toggle("active", !playlistsActive);
	playlistsTab.classList.toggle("active", playlistsActive);
	libraryViews.forEach((el) => {
		el.hidden = playlistsActive;
	});
	playlistsView.hidden = !playlistsActive;
	if (playlistsActive && load) {
		loadPlaylists(selectedPlaylistID).catch(console.error);
	}
}

async function loadPlaylists(selectID = selectedPlaylistID) {
	playlists = await api("/api/playlists");
	if (!playlists.some((playlist) => playlist.id === selectID)) {
		selectID = playlists[0]?.id || 0;
	}
	selectedPlaylistID = selectID;
	storageSet(playlistStorageKey, selectedPlaylistID || "");
	renderPlaylists();
	if (selectedPlaylistID) {
		await loadPlaylistDetail(selectedPlaylistID);
	} else {
		playlistDetailEl.replaceChildren(emptyHint("No playlists yet"));
	}
	runSearch().catch(console.error);
}

function renderPlaylists() {
	playlistSelect.replaceChildren(...playlists.map((playlist) => {
		const option = document.createElement("option");
		option.value = String(playlist.id);
		option.textContent = playlist.name;
		return option;
	}));
	playlistSelect.hidden = playlists.length === 0;
	playlistSelect.value = selectedPlaylistID ? String(selectedPlaylistID) : "";
	updatePlaylistActionButtons();
}

async function loadPlaylistDetail(id) {
	const playlist = await api(`/api/playlists/${id}`);
	renderPlaylistDetail(playlist);
}

function renderPlaylistItem(playlist, item) {
	const dedupeKey = item.dedupe_key || "";
	const track = {
		dedupe_key: dedupeKey,
		title: item.title || "Unknown track",
		artist: item.artist || "",
		album: item.album || "",
	};
	const extraButtons = [];
	if (playlist.can_edit) {
		const remove = trashButton("Remove from playlist", async () => {
			const updated = await api(`/api/playlists/${playlist.id}/items/${item.id}`, {method: "DELETE"});
			renderPlaylistDetail(updated);
		});
		extraButtons.push(remove);
	}
	return trackRow(track, standardTrackCommands(dedupeKey), "", dedupeKey, extraButtons);
}

function renderPlaylistDetail(playlist) {
	const items = playlist.items || [];
	const list = document.createElement("div");
	list.className = "playlist-items";
	list.replaceChildren(...(items.length ? items.map((item) => renderPlaylistItem(playlist, item)) : [emptyHint("No tracks in this playlist")]));
	playlistDetailEl.replaceChildren(list);
	playlists = playlists.map((existing) => existing.id === playlist.id ? playlist : existing);
	updatePlaylistActionButtons();
}

function updatePlaylistActionButtons() {
	const playlist = playlists.find((item) => item.id === selectedPlaylistID);
	const canRun = Boolean(playlist) && hasRoomPermission("queue_add");
	queuePlaylistButton.hidden = !canRun;
	shufflePlaylistButton.hidden = !canRun;
	deletePlaylistButton.hidden = !playlist?.can_edit;
}

function emptyHint(text) {
	const hint = document.createElement("p");
	hint.className = "hint";
	hint.textContent = text;
	return hint;
}

async function loadRooms() {
  const info = await api("/api/session");
  currentUserEl.textContent = info.user?.username || "Signed in";
  const rooms = info.rooms || [];
  if (!currentRoomID) {
    currentRoomID = info.default_room_id || (rooms[0] && rooms[0].id) || "main";
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
  currentPermissions = new Set(info.permissions?.[currentRoomID] || []);
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
  resultsEl.replaceChildren(...tracks.map((track) => trackRow(track, standardTrackCommands(track.dedupe_key))));
}

libraryTab.addEventListener("click", () => setRailMode("library"));
playlistsTab.addEventListener("click", () => setRailMode("playlists"));

playlistSelect.addEventListener("change", async () => {
	selectedPlaylistID = Number(playlistSelect.value);
	if (!selectedPlaylistID) return;
	storageSet(playlistStorageKey, selectedPlaylistID);
	await loadPlaylistDetail(selectedPlaylistID);
	runSearch().catch(console.error);
});

queuePlaylistButton.addEventListener("click", async () => {
	if (!selectedPlaylistID) return;
	await command({action: "playlist_queue", playlist_id: selectedPlaylistID});
});

shufflePlaylistButton.addEventListener("click", async () => {
	if (!selectedPlaylistID) return;
	await command({action: "playlist_shuffle", playlist_id: selectedPlaylistID});
});

deletePlaylistButton.addEventListener("click", async () => {
	const playlist = playlists.find((item) => item.id === selectedPlaylistID);
	if (!playlist?.can_edit || !confirm(`Delete playlist "${playlist.name}"?`)) return;
	await api(`/api/playlists/${playlist.id}`, {method: "DELETE"});
	selectedPlaylistID = 0;
	await loadPlaylists(0);
});

newPlaylistButton.addEventListener("click", () => {
	const open = playlistCreatePanel.hidden;
	playlistCreatePanel.hidden = !open;
	if (open) {
		playlistNameInput.focus();
	}
});

playlistCreateForm.addEventListener("submit", async (event) => {
	event.preventDefault();
	const name = playlistNameInput.value.trim();
	if (!name) return;
	const playlist = await api("/api/playlists", {
		method: "POST",
		body: JSON.stringify({name}),
	});
	playlistNameInput.value = "";
	playlistCreatePanel.hidden = true;
	await loadPlaylists(playlist.id);
});

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
  closePlaylistAddMenus();
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
  closePlaylistAddMenus();
  listenerListEl.hidden = true;
  presenceButton.setAttribute("aria-expanded", "false");
});

restoreSearchPreferences();
restoreRailPreferences();
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
	initQueueSortable();
  closeEvents();
  events = new EventSource(`/rooms/${encodeURIComponent(currentRoomID)}/events`);
  events.addEventListener("state", (event) => {
    renderState(JSON.parse(event.data));
  });
	loadLibraryStatus();
	loadPlaylists().catch(console.error);
	runSearch().catch(console.error);
	api(roomAPI("/api/state")).then(renderState).catch(console.error);
}

start().catch(console.error);
