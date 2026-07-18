import {
  currentRoomID,
  currentPermissions,
  canAdministerCurrentRoom,
  lastState,
  setLastState,
  setLastStateReceivedAt,
  queueDragActive,
  setQueueDragActive,
  queueReorderPending,
  setQueueReorderPending,
  pendingQueueState,
  setPendingQueueState,
  setCurrentRoomID,
  setCurrentPermissions,
  setCanAdministerCurrentRoom,
  roomAPI,
} from "./state.js";
import apiModule from "./api.js";
import audioModule from "./audio.js";
import roomSettingsModule from "./room-settings.js";
import autoDJModule from "./auto-dj.js";
import volumeModule from "./volume.js";
import queueModule from "./queue.js";
import renderStateModule from "./render-state.js";

let roomSelect, currentUserEl, logoutForm, roomSettingsButton, audioEl;

function init() {
  roomSelect = document.getElementById("roomSelect");
  currentUserEl = document.getElementById("currentUser");
  logoutForm = document.getElementById("logoutForm");
  roomSettingsButton = document.getElementById("roomSettingsButton");
  audioEl = document.getElementById("audio");

  roomSelect.addEventListener("change", () => {
    switchRoom(roomSelect.value).catch(console.error);
  });

  window.addEventListener("popstate", () => {
    const roomID = decodeURIComponent(
      location.pathname.match(/^\/rooms\/([^/]+)/)?.[1] || "",
    );
    if (roomID) switchRoom(roomID, false).catch(console.error);
  });

  logoutForm.addEventListener("submit", () => {
    audioModule.closeEvents();
  });

  window.addEventListener("pagehide", audioModule.closeEvents);
}

async function loadRooms(info = null) {
  info ||= await apiModule.api("/api/session");
  currentUserEl.textContent =
    info.user?.display_name || info.user?.username || "Signed in";
  const rooms = info.rooms || [];
  if (!currentRoomID) {
    setCurrentRoomID(
      info.default_room_id || (rooms[0] && rooms[0].id) || "main",
    );
  }
  if (rooms.length > 0 && !rooms.some((room) => room.id === currentRoomID)) {
    setCurrentRoomID(rooms[0].id);
  }
  roomSelect.replaceChildren(
    ...rooms.map((room) => {
      const option = document.createElement("option");
      option.value = room.id;
      option.textContent = room.name || room.id;
      return option;
    }),
  );
  roomSelect.value = currentRoomID;
  roomSelect.disabled = rooms.length <= 1;
  setCurrentPermissions(new Set(info.permissions?.[currentRoomID] || []));
  if (info.disconnected?.[currentRoomID]) {
    audioModule.forceLogout();
    return false;
  }
  setCanAdministerCurrentRoom(
    Boolean(info.room_administration?.[currentRoomID]),
  );
  roomSettingsButton.hidden = !canAdministerCurrentRoom;
  if (!canAdministerCurrentRoom) roomSettingsModule.closeRoomSettings();
  return true;
}

async function switchRoom(roomID, updateHistory = true) {
  if (!roomID || roomID === currentRoomID) return;
  roomSelect.disabled = true;
  try {
    const [info, state] = await Promise.all([
      apiModule.api("/api/session"),
      apiModule.api(`/rooms/${encodeURIComponent(roomID)}/api/state`),
    ]);
    if (!(info.rooms || []).some((room) => room.id === roomID)) {
      throw new Error("room not found");
    }

    audioModule.closeEvents();
    audioEl.pause();
    roomSettingsModule.closeRoomSettings();
    autoDJModule.closeAutoDJSourceMenu();
    setCurrentRoomID(roomID);
    setLastState(null);
    setLastStateReceivedAt(0);
    setQueueDragActive(false);
    setQueueReorderPending(false);
    setPendingQueueState(null);
    setCanAdministerCurrentRoom(false);
    if (updateHistory)
      history.pushState(null, "", `/rooms/${encodeURIComponent(roomID)}`);

    if (!(await loadRooms(info))) return;
    volumeModule.restoreVolumePreferences();
    renderStateModule.renderState(state);
    audioModule.connectEvents();
  } catch (err) {
    console.error(err);
    roomSelect.value = currentRoomID;
    history.replaceState(
      null,
      "",
      `/rooms/${encodeURIComponent(currentRoomID)}`,
    );
  } finally {
    roomSelect.disabled = roomSelect.options.length <= 1;
    queueModule.updateQueueSortable();
  }
}

export default { init, loadRooms, switchRoom };
