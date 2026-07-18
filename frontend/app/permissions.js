import { currentPermissions } from "./state.js";
import playlists from "./playlists.js";

function hasRoomPermission(permission) {
  return currentPermissions.has(permission);
}

function canRunCommand(action) {
  if (
    ["play", "play_now", "pause", "previous", "seek", "skip"].includes(action)
  ) {
    return hasRoomPermission("playback_control");
  }
  if (action === "queue_add") {
    return hasRoomPermission("queue_add");
  }
  return hasRoomPermission("queue_manage");
}

function refreshPermissionControls() {
  document.querySelectorAll("[data-room-action]").forEach((button) => {
    button.hidden = !canRunCommand(button.dataset.roomAction);
  });
  document
    .querySelectorAll(".item .row-actions")
    .forEach(updateRowActionLayout);
  playlists.updatePlaylistActionButtons();
}

function updateRowActionLayout(actions) {
  const visibleRoomActions = [
    ...actions.querySelectorAll("[data-room-action]"),
  ].filter((button) => !button.hidden);
  const hasRoomActions = visibleRoomActions.length > 0;
  const hasPlaylistAction = Boolean(
    actions.querySelector(".playlist-more-button"),
  );
  const hasStandaloneAction = [...actions.children].some(
    (element) =>
      element.matches("button:not([data-room-action])") && !element.hidden,
  );
  actions.classList.toggle(
    "playlist-only",
    !hasRoomActions && hasPlaylistAction,
  );
  actions.classList.toggle(
    "no-actions",
    !hasRoomActions && !hasPlaylistAction && !hasStandaloneAction,
  );
  actions.classList.toggle(
    "single-room-action",
    visibleRoomActions.length === 1,
  );
  actions.classList.toggle("has-standalone-action", hasStandaloneAction);
  actions.classList.toggle(
    "standalone-only",
    !hasRoomActions && !hasPlaylistAction && hasStandaloneAction,
  );
}

export default {
  hasRoomPermission,
  canRunCommand,
  refreshPermissionControls,
  updateRowActionLayout,
};
