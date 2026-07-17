import { currentRoomID, roomAPI, storageSet, searchTextStorageKey, searchFieldStorageKey, railModeStorageKey } from "./app/state.js";
import renderStateModule from "./app/render-state.js";
import volume from "./app/volume.js";
import seek from "./app/seek.js";
import audio from "./app/audio.js";
import queue from "./app/queue.js";
import search from "./app/search.js";
import playlists from "./app/playlists.js";
import roomSettings from "./app/room-settings.js";
import presence from "./app/presence.js";
import autoDJ from "./app/auto-dj.js";
import room from "./app/room.js";
import apiModule from "./app/api.js";

audio.setRenderState(renderStateModule.renderState);

volume.init();
seek.init();
audio.init();
queue.init();
search.init();
playlists.init();
roomSettings.init();
room.init();
presence.init();
autoDJ.init();

search.restoreSearchPreferences();
playlists.restoreRailPreferences();
seek.renderPlaybackButton(false);
volume.applyAudioSettings(0, false);

const queueChangesListEl = document.getElementById("queueChangesList");
const queueChangesButton = document.getElementById("queueChangesButton");
const listenerListEl = document.getElementById("listenerList");
const presenceButton = document.getElementById("presenceButton");
const roomSettingsView = document.getElementById("roomSettingsView");

document.addEventListener("click", (event) => {
  playlists.closePlaylistAddMenus();
  if (!event.target.closest(".auto-dj-control")) autoDJ.closeAutoDJSourceMenu();
  if (!event.target.closest(".queue-changes-menu")) {
    queueChangesListEl.hidden = true;
    queueChangesButton.setAttribute("aria-expanded", "false");
  }
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
  playlists.closePlaylistAddMenus();
  autoDJ.closeAutoDJSourceMenu();
  if (!roomSettingsView.hidden) roomSettings.closeRoomSettings();
  listenerListEl.hidden = true;
  presenceButton.setAttribute("aria-expanded", "false");
  queueChangesListEl.hidden = true;
  queueChangesButton.setAttribute("aria-expanded", "false");
});

async function start() {
  if (!await room.loadRooms()) {
    return;
  }
	history.replaceState(null, "", `/rooms/${encodeURIComponent(currentRoomID)}`);
	volume.restoreVolumePreferences();
	queue.initQueueSortable();
	audio.connectEvents();
	playlists.loadLibraryStatus();
	playlists.loadPlaylists().catch(console.error);
	search.runSearch().catch(console.error);
	apiModule.api(roomAPI("/api/state")).then(renderStateModule.renderState).catch(console.error);
}

start().catch(console.error);
