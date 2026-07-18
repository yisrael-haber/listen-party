import {
  canAdministerCurrentRoom,
  roomAPI,
  roomSaveFeedbackTimer,
  minimumRoomSaveFeedbackMS,
  roomSaveResultVisibleMS,
  setRoomSaveFeedbackTimer,
} from "./state.js";
import apiModule from "./api.js";
import renderStateModule from "./render-state.js";

let roomSettingsView,
  roomSettingsGrants,
  roomSettingsOverrides,
  roomSettingsStatus,
  saveRoomSettingsButton,
  addUserOverrideButton,
  roomSettingsButton,
  closeRoomSettingsButton,
  libraryPanel,
  roomSettingsRowTemplate,
  roomSettingsUsers = [];

function init() {
  roomSettingsView = document.getElementById("roomSettingsView");
  roomSettingsGrants = document.getElementById("roomSettingsGrants");
  roomSettingsOverrides = document.getElementById("roomSettingsOverrides");
  roomSettingsStatus = document.getElementById("roomSettingsStatus");
  saveRoomSettingsButton = document.getElementById("saveRoomSettings");
  addUserOverrideButton = document.getElementById("addUserOverride");
  roomSettingsButton = document.getElementById("roomSettingsButton");
  closeRoomSettingsButton = document.getElementById("closeRoomSettings");
  libraryPanel = document.getElementById("libraryPanel");
  roomSettingsRowTemplate = document.getElementById(
    "roomSettingsPermissionRow",
  );

  roomSettingsButton.addEventListener("click", toggleRoomSettings);
  closeRoomSettingsButton.addEventListener("click", closeRoomSettings);
  addUserOverrideButton.addEventListener("click", () => {
    const row = roomOverrideRow();
    roomSettingsOverrides.append(row);
    row.querySelector("select").focus();
  });

  saveRoomSettingsButton.addEventListener("click", async () => {
    clearTimeout(roomSaveFeedbackTimer);
    saveRoomSettingsButton.disabled = true;
    saveRoomSettingsButton.textContent = "Saving...";
    roomSettingsStatus.textContent = "Saving...";
    roomSettingsStatus.title = "";
    const saveRequest = apiModule
      .api(roomAPI("/api/admin"), {
        method: "PUT",
        body: JSON.stringify({
          grants: readRoomSettingsGrants(),
          user_overrides: readRoomSettingsOverrides(),
        }),
      })
      .then(
        (settings) => ({ settings }),
        (error) => ({ error }),
      );
    const [result] = await Promise.all([
      saveRequest,
      new Promise((resolve) => setTimeout(resolve, minimumRoomSaveFeedbackMS)),
    ]);
    if (result.error) {
      saveRoomSettingsButton.textContent = "Failed";
      roomSettingsStatus.textContent = "Failed";
      roomSettingsStatus.title = result.error.message || "Save failed";
    } else {
      renderRoomSettings(result.settings);
      saveRoomSettingsButton.textContent = "Saved";
      roomSettingsStatus.textContent = "Saved";
      roomSettingsStatus.title = "";
      apiModule
        .api(roomAPI("/api/state"))
        .then(renderStateModule.renderState)
        .catch(console.error);
    }
    setRoomSaveFeedbackTimer(
      setTimeout(() => {
        saveRoomSettingsButton.disabled = false;
        saveRoomSettingsButton.textContent = "Save";
        roomSettingsStatus.textContent = "";
        roomSettingsStatus.title = "";
      }, roomSaveResultVisibleMS),
    );
  });
}

async function openRoomSettings() {
  if (!canAdministerCurrentRoom) {
    return;
  }
  libraryPanel.hidden = true;
  roomSettingsView.hidden = false;
  roomSettingsButton.setAttribute("aria-expanded", "true");
  try {
    await loadRoomSettings();
  } catch (err) {
    roomSettingsStatus.textContent =
      err.message || "Could not load room settings";
  }
}

function closeRoomSettings() {
  roomSettingsView.hidden = true;
  libraryPanel.hidden = false;
  roomSettingsButton.setAttribute("aria-expanded", "false");
}

function toggleRoomSettings() {
  if (roomSettingsView.hidden) {
    openRoomSettings();
  } else {
    closeRoomSettings();
  }
}

function roomGrantRow(group, permissions = [], builtIn = false) {
  const row = roomPermissionRow(permissions, !builtIn);
  row.querySelector("[data-room-settings-subject-label]").textContent = builtIn
    ? "Default access"
    : "PocketBase group";
  const input = row.querySelector("[data-room-settings-subject]");
  input.className = "room-settings-group";
  input.value = builtIn ? "Everyone" : group;
  input.dataset.group = builtIn ? "everyone" : "";
  input.placeholder = "PocketBase group";
  input.readOnly = builtIn;
  return row;
}

function roomOverrideRow(userID = "", permissions = []) {
  const row = roomPermissionRow(permissions);
  row.classList.add("room-settings-override");
  row.querySelector("[data-room-settings-subject-label]").textContent = "User";
  const select = document.createElement("select");
  select.className = "room-settings-user";
  select.add(new Option("Select a user", ""));
  for (const user of roomSettingsUsers) {
    select.add(new Option(userDisplayLabel(user), user.id));
  }
  if (userID && !roomSettingsUsers.some((user) => user.id === userID)) {
    select.add(new Option("Unavailable user", userID));
  }
  select.value = userID;
  row.querySelector("[data-room-settings-subject]").replaceWith(select);
  return row;
}

function roomPermissionRow(permissions = [], removable = true) {
  const row = roomSettingsRowTemplate.content.firstElementChild.cloneNode(true);
  const remove = row.querySelector("[data-room-settings-remove]");
  if (removable) {
    remove.addEventListener("click", () => row.remove());
  } else {
    remove.remove();
  }
  for (const checkbox of row.querySelectorAll("[data-permission]")) {
    checkbox.checked = permissions.includes(checkbox.dataset.permission);
    checkbox.parentElement.classList.toggle("checked", checkbox.checked);
    checkbox.addEventListener("change", () => {
      checkbox.parentElement.classList.toggle("checked", checkbox.checked);
    });
  }
  return row;
}

function userDisplayLabel(user) {
  return user.display_name
    ? `${user.display_name} (${user.username})`
    : user.username;
}

function renderRoomSettings(settings) {
  const grants = settings.grants || {};
  const overrides = settings.user_overrides || {};
  const grantsList = document.createElement("div");
  grantsList.className = "room-settings-grants";
  grantsList.append(
    roomGrantRow("everyone", grants.everyone || [], true),
    ...Object.entries(grants)
      .filter(([group]) => group !== "everyone")
      .map(([group, permissions]) => roomGrantRow(group, permissions)),
  );
  const addGrant = document.createElement("button");
  addGrant.className = "secondary compact room-settings-add";
  addGrant.type = "button";
  addGrant.textContent = "Add group";
  addGrant.addEventListener("click", () => {
    const row = roomGrantRow("", []);
    grantsList.append(row);
    row.querySelector("input").focus();
  });

  roomSettingsGrants.replaceChildren(grantsList, addGrant);
  roomSettingsOverrides.replaceChildren(
    ...Object.entries(overrides).map(([userID, permissions]) =>
      roomOverrideRow(userID, permissions),
    ),
  );
}

function readRoomSettingsGrants() {
  return readRoomSettingsRows(
    roomSettingsGrants,
    ".room-settings-grant:not(.room-settings-override)",
    (row) => {
      const input = row.querySelector(".room-settings-group");
      return (input.dataset.group || input.value).trim();
    },
  );
}

function readRoomSettingsOverrides() {
  return readRoomSettingsRows(
    roomSettingsOverrides,
    ".room-settings-override",
    (row) => row.querySelector(".room-settings-user").value,
    true,
  );
}

function readRoomSettingsRows(
  container,
  selector,
  readSubject,
  allowEmptyPermissions = false,
) {
  const values = {};
  for (const row of container.querySelectorAll(selector)) {
    const subject = readSubject(row);
    const permissions = readPermissions(row);
    if (subject && (allowEmptyPermissions || permissions.length)) {
      values[subject] = permissions;
    }
  }
  return values;
}

function readPermissions(row) {
  return [...row.querySelectorAll("[data-permission]:checked")].map(
    (checkbox) => checkbox.dataset.permission,
  );
}

async function loadRoomSettings() {
  roomSettingsStatus.textContent = "Loading...";
  const settings = await apiModule.api(roomAPI("/api/admin"));
  roomSettingsUsers = settings.users || [];
  renderRoomSettings(settings);
  roomSettingsStatus.textContent = "";
}

export default {
  init,
  openRoomSettings,
  closeRoomSettings,
  toggleRoomSettings,
  roomGrantRow,
  roomOverrideRow,
  renderRoomSettings,
  readRoomSettingsGrants,
  readRoomSettingsOverrides,
  loadRoomSettings,
};
