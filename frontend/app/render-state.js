import { lastState, setLastState, setLastStateReceivedAt, currentRoomID, setPendingQueueState, queueDragActive, queueReorderPending, pendingQueueState, setCurrentPermissions } from "./state.js";
import audioModule from "./audio.js";
import seekModule from "./seek.js";
import volumeModule from "./volume.js";
import formatting from "./formatting.js";
import trackUi from "./track-ui.js";
import queueModule from "./queue.js";
import historyModule from "./history.js";
import presenceModule from "./presence.js";
import autoDJModule from "./auto-dj.js";
import permissions from "./permissions.js";


const audioEl = document.getElementById("audio");
const trackEl = document.getElementById("track");
const artistEl = document.getElementById("artist");
const queueEl = document.getElementById("queue");
const clearQueueButton = document.getElementById("clearQueue");
const autoDJButton = document.getElementById("autoDJ");
const autoDJSourceButton = document.getElementById("autoDJSource");
const clearHistoryButton = document.getElementById("clearHistory");
const previousButton = document.getElementById("previous");
const skipButton = document.getElementById("skip");
const togglePlaybackButton = document.getElementById("togglePlayback");

function renderState(state) {
  const revision = Number(state?.revision);
  const serverTime = Date.parse(state?.server_time);
  if (typeof state?.generation !== "string" || !state.generation || !Number.isSafeInteger(revision) || revision < 0 || !Number.isFinite(serverTime)) {
    audioModule.recoverPlaybackClient("malformed playback state");
    return;
  }
  if (state.room_id && currentRoomID && state.room_id !== currentRoomID) {
    return;
  }
  if (lastState) {
    if (state.generation !== lastState.generation) {
      // A backend restart has a new generation. Reopen the media stream and
      // render the restored state directly instead of reloading the page.
      audioEl.pause();
      audioEl.removeAttribute("src");
      audioEl.load();
      audioModule.clearArtwork();
      setLastState(null);
      setLastStateReceivedAt(0);
      setPendingQueueState(null);
    } else {
      const lastRevision = Number(lastState.revision);
      const lastServerTime = Date.parse(lastState.server_time);
      if (revision < lastRevision) return;
      if (revision === lastRevision && serverTime < lastServerTime) return;
    }
  }
  if (queueDragActive || queueReorderPending) {
    if (!pendingQueueState || Date.parse(state.server_time) >= Date.parse(pendingQueueState.server_time)) {
      setPendingQueueState(state);
    }
    return;
  }
  if (Array.isArray(state.permissions)) {
    setCurrentPermissions(new Set(state.permissions));
  }

  const timelineChanged = !audioModule.samePlaybackTimeline(lastState, state);
  setLastState(state);
  setLastStateReceivedAt(Date.now());

  const queue = state.queue || [];
  const history = state.history || [];
  const current = state.current;
  const currentTrack = current?.track;
  if (!currentTrack) {
    audioEl.pause();
    audioEl.removeAttribute("src");
    audioEl.load();
    audioModule.clearArtwork();
    seekModule.setSeekUI(0);
    trackEl.textContent = "Nothing playing";
    artistEl.textContent = "";
  } else {
    trackEl.textContent = formatting.trackTitle(currentTrack);
    trackUi.renderSubtitle(artistEl, formatting.trackContext(currentTrack), formatting.playbackRequester(current));
	audioModule.loadMedia(currentTrack);
    audioModule.syncAudio(state, timelineChanged);
  }

  queueEl.replaceChildren(...(queue.length ? queue.map(queueModule.renderQueueItem) : [formatting.emptyHint("Queue is empty", "li")]));
  historyModule.renderHistory(history);
  const canManageQueue = permissions.hasRoomPermission("queue_manage");
  const canControlPlayback = permissions.hasRoomPermission("playback_control");
  clearQueueButton.hidden = !canManageQueue || queue.length === 0;
  const autoDJ = state.auto_dj || {enabled: false, source: {type: "library", name: "Entire Library"}};
  autoDJButton.disabled = !canManageQueue;
  autoDJSourceButton.disabled = !canManageQueue;
  if (!canManageQueue) autoDJModule.closeAutoDJSourceMenu();
  autoDJButton.dataset.enabled = String(Boolean(autoDJ.enabled));
  autoDJButton.setAttribute("aria-pressed", String(Boolean(autoDJ.enabled)));
  autoDJButton.title = autoDJ.enabled ? "Disable Auto-DJ" : "Enable Auto-DJ";
  autoDJButton.setAttribute("aria-label", autoDJButton.title);
  autoDJSourceButton.textContent = autoDJ.source?.name || "Entire Library";
  autoDJSourceButton.title = `Auto-DJ source: ${autoDJSourceButton.textContent}`;
  volumeModule.renderVolumeControl();
  clearHistoryButton.hidden = !canManageQueue || history.length === 0;
  presenceModule.renderPresence(state);
  queueModule.renderQueueChanges(state.actions || []);
  previousButton.disabled = !canControlPlayback || history.length === 0;
  skipButton.disabled = !canControlPlayback;
  togglePlaybackButton.disabled = !canControlPlayback || (!currentTrack && queue.length === 0);
  permissions.refreshPermissionControls();
  queueModule.updateQueueSortable();
  seekModule.renderPlaybackButton(Boolean(currentTrack && !state.paused));
}

export default { renderState };
