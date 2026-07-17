export const defaultVolume = 0.25;
export const syncToleranceSeconds = 0.3;
export const searchDebounceMS = 300;
export const searchTextStorageKey = "listen-party.searchText";
export const searchFieldStorageKey = "listen-party.searchField";
export const railModeStorageKey = "listen-party.railMode";
export const playlistStorageKey = "listen-party.selectedPlaylist";
export const localVolumeStorageKey = "listen-party.localVolume";
export const localMutedStorageKey = "listen-party.localMuted";
export const minimumRoomSaveFeedbackMS = 450;
export const roomSaveResultVisibleMS = 1400;
export const recoveryStorageKey = "listen-party.playbackRecoveryAt";
export const recoveryCooldownMS = 30000;

export let lastState = null;
export let lastStateReceivedAt = 0;
export let searchTimer = 0;
export let seeking = false;
export let events = null;
export let playlists = [];
export let selectedPlaylistID = 0;
export let currentPermissions = new Set();
export let queueSortable = null;
export let queueDragActive = false;
export let queueReorderPending = false;
export let pendingQueueState = null;
export let canAdministerCurrentRoom = false;
export let roomSaveFeedbackTimer = 0;
export let volumeMode = "local";
export let localVolume = 0;
export let localMuted = false;
export let currentRoomID = decodeURIComponent(location.pathname.match(/^\/rooms\/([^/]+)/)?.[1] || "");

export function setLastState(value) { lastState = value; }
export function setLastStateReceivedAt(value) { lastStateReceivedAt = value; }
export function setSearchTimer(value) { searchTimer = value; }
export function setSeeking(value) { seeking = value; }
export function setEvents(value) { events = value; }
export function setPlaylists(value) { playlists = value; }
export function setSelectedPlaylistID(value) { selectedPlaylistID = value; }
export function setCurrentPermissions(value) { currentPermissions = value; }
export function setQueueSortable(value) { queueSortable = value; }
export function setQueueDragActive(value) { queueDragActive = value; }
export function setQueueReorderPending(value) { queueReorderPending = value; }
export function setPendingQueueState(value) { pendingQueueState = value; }
export function setCanAdministerCurrentRoom(value) { canAdministerCurrentRoom = value; }
export function setRoomSaveFeedbackTimer(value) { roomSaveFeedbackTimer = value; }
export function setVolumeMode(value) { volumeMode = value; }
export function setLocalVolume(value) { localVolume = value; }
export function setLocalMuted(value) { localMuted = value; }
export function setCurrentRoomID(value) { currentRoomID = value; }

export function storageGet(key) {
  try {
    return localStorage.getItem(key) || "";
  } catch {
    return "";
  }
}

export function storageSet(key, value) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // Persistence is optional; private browsing or storage policies may reject it.
  }
}

export function roomAPI(path) {
  return `/rooms/${encodeURIComponent(currentRoomID)}${path}`;
}
