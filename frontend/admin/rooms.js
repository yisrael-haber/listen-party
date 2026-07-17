import {
  roomsList,
  roomCounter,
  setRoomCounter,
} from "./state.js";
import {
  renderListItem,
  updateListRemoveButtons,
  listEditor,
} from "./list-editor.js";

export function renderRooms(rooms) {
  roomsList.replaceChildren(...rooms.map(renderRoomRow));
  updateRoomRemoveButtons();
}

export function renderRoomRow(room = {}) {
  const row = document.createElement("div");
  row.className = "room-row";
  row.roomGrants = cloneGrants(room.grants || {});
  const fields = document.createElement("div");
  fields.className = "room-fields";
  const main = document.createElement("div");
  main.className = "room-main-row";
  const access = document.createElement("div");
  access.className = "room-access-row";

  const id = inputField("ID", "room-id", room.id || `room-${roomCounter}`);
  setRoomCounter(roomCounter + 1);
  const name = inputField("Name", "room-name", room.name || "New Room");

  const remove = document.createElement("button");
  remove.className = "secondary compact icon-only trash-button room-remove";
  remove.type = "button";
  remove.title = "Remove room";
  remove.setAttribute("aria-label", "Remove room");
  remove.append(document.createElement("span"));
  remove.addEventListener("click", () => {
    row.remove();
    updateRoomRemoveButtons();
  });

  main.append(id, name);
  access.append(listEditor("Room administrator groups", "room-admin-group", room.admin_groups || [], "Group"));
  fields.append(main, access);
  row.append(fields, remove);
  return row;
}

export function readRooms() {
  return [...roomsList.querySelectorAll(".room-row")].map((row) => ({
    id: row.querySelector(".room-id").value.trim(),
    name: row.querySelector(".room-name").value.trim(),
    admin_groups: [...row.querySelectorAll(".room-admin-group")].map((input) => input.value.trim()).filter(Boolean),
    grants: cloneGrants(row.roomGrants),
  }));
}

export function updateRoomRemoveButtons() {
  const buttons = roomsList.querySelectorAll(".room-remove");
  buttons.forEach((button) => {
    button.disabled = buttons.length <= 1;
  });
}

export function cloneGrants(grants) {
  return Object.fromEntries(Object.entries(grants || {}).map(([group, permissions]) => [group, [...permissions]]));
}

export function inputField(labelText, className, value) {
  const label = document.createElement("label");
  label.className = "room-field";
  const span = document.createElement("span");
  span.textContent = labelText;
  const input = document.createElement("input");
  input.className = className;
  input.value = value;
  input.autocomplete = "off";
  label.append(span, input);
  return label;
}

export function init() {
  const addRoomButton = document.getElementById("addRoom");
  addRoomButton.addEventListener("click", () => {
    roomsList.append(renderRoomRow());
    updateRoomRemoveButtons();
  });
}
