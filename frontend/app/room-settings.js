import { canAdministerCurrentRoom, currentRoomID, roomAPI, roomSaveFeedbackTimer, minimumRoomSaveFeedbackMS, roomSaveResultVisibleMS, setRoomSaveFeedbackTimer } from "./state.js";
import apiModule from "./api.js";
import renderStateModule from "./render-state.js";

let roomSettingsView, roomSettingsGrants, roomSettingsStatus, saveRoomSettingsButton, roomSettingsButton, closeRoomSettingsButton, libraryPanel;

function init() {
  roomSettingsView = document.getElementById("roomSettingsView");
  roomSettingsGrants = document.getElementById("roomSettingsGrants");
  roomSettingsStatus = document.getElementById("roomSettingsStatus");
  saveRoomSettingsButton = document.getElementById("saveRoomSettings");
  roomSettingsButton = document.getElementById("roomSettingsButton");
  closeRoomSettingsButton = document.getElementById("closeRoomSettings");
  libraryPanel = document.getElementById("libraryPanel");

  roomSettingsButton.addEventListener("click", toggleRoomSettings);
  closeRoomSettingsButton.addEventListener("click", closeRoomSettings);

  saveRoomSettingsButton.addEventListener("click", async () => {
    clearTimeout(roomSaveFeedbackTimer);
    saveRoomSettingsButton.disabled = true;
    saveRoomSettingsButton.textContent = "Saving...";
    roomSettingsStatus.textContent = "Saving...";
    roomSettingsStatus.title = "";
    const saveRequest = apiModule.api(roomAPI("/api/admin/grants"), {
            method: "PUT",
            body: JSON.stringify({grants: readRoomSettingsGrants()}),
        }).then((settings) => ({settings}), (error) => ({error}));
    const [result] = await Promise.all([saveRequest, new Promise((resolve) => setTimeout(resolve, minimumRoomSaveFeedbackMS))]);
    if (result.error) {
        saveRoomSettingsButton.textContent = "Failed";
        roomSettingsStatus.textContent = "Failed";
        roomSettingsStatus.title = result.error.message || "Save failed";
    } else {
        const settings = result.settings;
        renderRoomSettings(settings.grants || {});
        saveRoomSettingsButton.textContent = "Saved";
        roomSettingsStatus.textContent = "Saved";
        roomSettingsStatus.title = "";
        apiModule.api(roomAPI("/api/state")).then(renderStateModule.renderState).catch(console.error);
    }
    setRoomSaveFeedbackTimer(setTimeout(() => {
        saveRoomSettingsButton.disabled = false;
        saveRoomSettingsButton.textContent = "Save";
        roomSettingsStatus.textContent = "";
        roomSettingsStatus.title = "";
    }, roomSaveResultVisibleMS));
  });
}

async function openRoomSettings() {
	if (!canAdministerCurrentRoom) return;
	libraryPanel.hidden = true;
	roomSettingsView.hidden = false;
	roomSettingsButton.setAttribute("aria-expanded", "true");
	try {
		await loadRoomSettings();
	} catch (err) {
		roomSettingsStatus.textContent = err.message || "Could not load room settings";
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
	const row = document.createElement("div");
	row.className = "room-settings-grant";
	const head = document.createElement("div");
	head.className = "room-settings-grant-head";
	const groupWrap = document.createElement("label");
	groupWrap.className = "room-settings-group-field";
	const groupLabel = document.createElement("span");
	groupLabel.textContent = builtIn ? "Default access" : "PocketBase group";
	const input = document.createElement("input");
	input.className = "room-settings-group";
	input.value = builtIn ? "Everyone" : group;
	input.dataset.group = builtIn ? "everyone" : "";
	input.placeholder = "PocketBase group";
	input.readOnly = builtIn;
	groupWrap.append(groupLabel, input);
	head.append(groupWrap);
	if (!builtIn) {
		const remove = document.createElement("button");
		remove.className = "secondary compact room-settings-remove";
		remove.type = "button";
		remove.textContent = "Remove";
		remove.addEventListener("click", () => row.remove());
		head.append(remove);
	}
	const permissionList = document.createElement("div");
	permissionList.className = "room-settings-permissions";
	for (const [value, labelText] of [
		["queue_add", "Add tracks to the queue"],
		["queue_manage", "Manage queued tracks"],
		["playback_control", "Control playback"],
		["volume_control", "Control room volume"],
	]) {
		const label = document.createElement("label");
		label.className = "checkbox-label";
		const checkbox = document.createElement("input");
		checkbox.type = "checkbox";
		checkbox.dataset.permission = value;
		checkbox.checked = permissions.includes(value);
		label.classList.toggle("checked", checkbox.checked);
		checkbox.addEventListener("change", () => {
			label.classList.toggle("checked", checkbox.checked);
		});
		const text = document.createElement("span");
		text.className = "permission-text";
		text.textContent = labelText;
		label.append(checkbox, text);
		permissionList.append(label);
	}
	row.append(head, permissionList);
	return row;
}

function renderRoomSettings(grants) {
	const entries = Object.entries(grants || {}).filter(([group]) => group !== "everyone");
	const list = document.createElement("div");
	list.className = "room-settings-grants";
	list.append(roomGrantRow("everyone", grants?.everyone || [], true), ...entries.map(([group, permissions]) => roomGrantRow(group, permissions)));
	const add = document.createElement("button");
	add.className = "secondary compact room-settings-add";
	add.type = "button";
	add.textContent = "Add group";
	add.addEventListener("click", () => {
		const row = roomGrantRow("", []);
		list.append(row);
		row.querySelector("input").focus();
	});
	roomSettingsGrants.replaceChildren(list, add);
}

function readRoomSettingsGrants() {
	const grants = {};
	for (const row of roomSettingsGrants.querySelectorAll(".room-settings-grant")) {
		const input = row.querySelector(".room-settings-group");
		const group = (input.dataset.group || input.value).trim();
		if (!group) continue;
		const permissions = [...row.querySelectorAll("[data-permission]:checked")].map((checkbox) => checkbox.dataset.permission);
		if (permissions.length) grants[group] = [...new Set(permissions)];
	}
	return grants;
}

async function loadRoomSettings() {
	roomSettingsStatus.textContent = "Loading...";
	const settings = await apiModule.api(roomAPI("/api/admin"));
	renderRoomSettings(settings.grants || {});
	roomSettingsStatus.textContent = "";
}

export default { init, openRoomSettings, closeRoomSettings, toggleRoomSettings, roomGrantRow, renderRoomSettings, readRoomSettingsGrants, loadRoomSettings };
