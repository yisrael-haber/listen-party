import { canAdministerCurrentRoom, currentRoomID, roomAPI } from "./state.js";
import apiModule from "./api.js";

let presenceEl, presenceButton, listenerListEl;

function init() {
  presenceEl = document.getElementById("presence");
  presenceButton = document.getElementById("presenceButton");
  listenerListEl = document.getElementById("listenerList");

  presenceButton.addEventListener("click", () => {
    const nextOpen = listenerListEl.hidden;
    listenerListEl.hidden = !nextOpen;
    presenceButton.setAttribute("aria-expanded", String(nextOpen));
  });
}

function renderPresence(state) {
  const listeners = Array.isArray(state.listeners) ? state.listeners : [];
  const count = listeners.length;
  presenceEl.textContent = `${count} listener${count === 1 ? "" : "s"}`;
  listenerListEl.replaceChildren(
    ...listeners.map((username) => {
      const item = document.createElement("div");
      item.className = "listener-item";
      const name = document.createElement("span");
      name.className = "listener-name";
      name.textContent = username;
      item.append(name);
      if (canAdministerCurrentRoom) {
        const disconnect = document.createElement("button");
        disconnect.className = "secondary compact listener-disconnect";
        disconnect.type = "button";
        disconnect.textContent = "Disconnect";
        disconnect.addEventListener("click", async () => {
          disconnect.disabled = true;
          try {
            await apiModule.api(roomAPI("/api/admin/disconnect"), {
              method: "POST",
              body: JSON.stringify({ username }),
            });
          } catch (err) {
            console.error(err);
            disconnect.disabled = false;
          }
        });
        item.append(disconnect);
      }
      return item;
    }),
  );
  if (listeners.length === 0) {
    const empty = document.createElement("div");
    empty.className = "listener-item empty";
    empty.textContent = "No active users";
    listenerListEl.append(empty);
  }
}

export default { init, renderPresence };
