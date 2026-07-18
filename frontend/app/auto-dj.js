import { lastState } from "./state.js";
import apiModule from "./api.js";

let autoDJButton, autoDJSourceButton, autoDJSourceMenu;

function init() {
  autoDJButton = document.getElementById("autoDJ");
  autoDJSourceButton = document.getElementById("autoDJSource");
  autoDJSourceMenu = document.getElementById("autoDJSourceMenu");

  autoDJButton.addEventListener("click", async () => {
    const enabled = autoDJButton.dataset.enabled !== "true";
    await apiModule.command({ action: "auto_dj", enabled });
  });

  autoDJSourceButton.addEventListener("click", async (event) => {
    event.stopPropagation();
    if (!autoDJSourceMenu.hidden) {
      closeAutoDJSourceMenu();
      return;
    }
    autoDJSourceMenu.replaceChildren();
    const loading = document.createElement("p");
    loading.className = "auto-dj-source-status";
    loading.textContent = "Loading sources...";
    autoDJSourceMenu.append(loading);
    autoDJSourceMenu.hidden = false;
    autoDJSourceButton.setAttribute("aria-expanded", "true");
    try {
      const availablePlaylists = await apiModule.api("/api/playlists");
      if (!autoDJSourceMenu.hidden) {
        renderAutoDJSourceMenu(availablePlaylists);
      }
    } catch (err) {
      console.error(err);
      loading.textContent = err.message || "Could not load shuffle sources";
    }
  });
}

function closeAutoDJSourceMenu() {
  autoDJSourceMenu.hidden = true;
  autoDJSourceButton.setAttribute("aria-expanded", "false");
}

function renderAutoDJSourceMenu(availablePlaylists) {
  const selected = lastState?.auto_dj?.source || { type: "library" };
  const sources = [
    { type: "library", name: "Entire Library" },
    ...availablePlaylists.map((playlist) => ({
      type: "playlist",
      playlist_id: playlist.id,
      name: playlist.name,
    })),
  ];
  autoDJSourceMenu.replaceChildren(
    ...sources.map((source) => {
      const active =
        source.type === selected.type &&
        (source.type !== "playlist" ||
          source.playlist_id === selected.playlist_id);
      const item = document.createElement("button");
      item.type = "button";
      item.className = "auto-dj-source-option";
      item.setAttribute("role", "menuitemradio");
      item.setAttribute("aria-checked", String(active));
      item.textContent = source.name;
      item.addEventListener("click", async () => {
        if (active) {
          closeAutoDJSourceMenu();
          return;
        }
        item.disabled = true;
        try {
          await apiModule.command({
            action: "auto_dj_source",
            source:
              source.type === "playlist"
                ? { type: "playlist", playlist_id: source.playlist_id }
                : { type: "library" },
          });
          closeAutoDJSourceMenu();
        } catch (err) {
          console.error(err);
          item.disabled = false;
          const error = document.createElement("p");
          error.className = "auto-dj-source-error";
          error.textContent = err.message || "Could not change Auto-DJ source";
          autoDJSourceMenu.append(error);
        }
      });
      return item;
    }),
  );
}

export default { init, closeAutoDJSourceMenu, renderAutoDJSourceMenu };
