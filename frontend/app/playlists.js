import {
  playlists,
  selectedPlaylistID,
  playlistStorageKey,
  railModeStorageKey,
  storageGet,
  storageSet,
  setPlaylists,
  setSelectedPlaylistID,
} from "./state.js";
import formatting from "./formatting.js";
import trackUi from "./track-ui.js";
import apiModule from "./api.js";
import searchModule from "./search.js";

let playlistSelect,
  deletePlaylistButton,
  newPlaylistButton,
  playlistCreatePanel,
  playlistCreateForm,
  playlistNameInput,
  importPlaylistFolderButton,
  playlistFolderInput,
  playlistImportStatus,
  libraryTab,
  playlistsTab,
  libraryViews,
  playlistsView,
  libraryStatus,
  playlistDetailEl;

function init() {
  playlistSelect = document.getElementById("playlistSelect");
  deletePlaylistButton = document.getElementById("deletePlaylist");
  newPlaylistButton = document.getElementById("newPlaylist");
  playlistCreatePanel = document.getElementById("playlistCreatePanel");
  playlistCreateForm = document.getElementById("playlistCreateForm");
  playlistNameInput = document.getElementById("playlistName");
  importPlaylistFolderButton = document.getElementById("importPlaylistFolder");
  playlistFolderInput = document.getElementById("playlistFolderInput");
  playlistImportStatus = document.getElementById("playlistImportStatus");
  libraryTab = document.getElementById("libraryTab");
  playlistsTab = document.getElementById("playlistsTab");
  libraryViews = document.querySelectorAll(".library-view");
  playlistsView = document.getElementById("playlistsView");
  libraryStatus = document.getElementById("libraryStatus");
  playlistDetailEl = document.getElementById("playlistDetail");

  libraryTab.addEventListener("click", () => setRailMode("library"));
  playlistsTab.addEventListener("click", () => setRailMode("playlists"));

  playlistSelect.addEventListener("change", async () => {
    playlistImportStatus.textContent = "";
    setSelectedPlaylistID(Number(playlistSelect.value));
    if (!selectedPlaylistID) {
      return;
    }
    storageSet(playlistStorageKey, selectedPlaylistID);
    await loadPlaylistDetail(selectedPlaylistID);
    searchModule.runSearch().catch(console.error);
  });

  deletePlaylistButton.addEventListener("click", async () => {
    const playlist = playlists.find((item) => item.id === selectedPlaylistID);
    if (
      !playlist?.can_edit ||
      !confirm(`Delete playlist "${playlist.name}"?`)
    ) {
      return;
    }
    await apiModule.api(`/api/playlists/${playlist.id}`, { method: "DELETE" });
    setSelectedPlaylistID(0);
    await loadPlaylists(0);
  });

  newPlaylistButton.addEventListener("click", () => {
    const open = playlistCreatePanel.hidden;
    playlistCreatePanel.hidden = !open;
    if (open) {
      playlistNameInput.focus();
    }
  });

  playlistCreateForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const name = playlistNameInput.value.trim();
    if (!name) {
      return;
    }
    const playlist = await apiModule.api("/api/playlists", {
      method: "POST",
      body: JSON.stringify({ name }),
    });
    playlistNameInput.value = "";
    playlistCreatePanel.hidden = true;
    await loadPlaylists(playlist.id);
  });

  importPlaylistFolderButton.addEventListener("click", () => {
    playlistImportStatus.textContent = "";
    playlistFolderInput.value = "";
    playlistFolderInput.click();
  });

  playlistFolderInput.addEventListener("change", async () => {
    const playlist = playlists.find((item) => item.id === selectedPlaylistID);
    if (!playlist?.can_edit) {
      return;
    }
    const files = [...playlistFolderInput.files]
      .filter((file) => file.name.toLowerCase().endsWith(".mp3"))
      .map((file) => ({
        relative_path: file.webkitRelativePath || file.name,
        size: file.size,
        last_modified_ms: file.lastModified,
      }));
    if (files.length === 0) {
      playlistImportStatus.textContent =
        "The selected folder contains no MP3 files";
      return;
    }
    importPlaylistFolderButton.disabled = true;
    playlistImportStatus.textContent = `Matching ${files.length} files...`;
    try {
      const result = await apiModule.api(
        `/api/playlists/${playlist.id}/import-folder`,
        {
          method: "POST",
          body: JSON.stringify({ files }),
        },
      );
      if (result.imported > 0) {
        playlistImportStatus.textContent = "Playlist imported";
      } else if (result.duplicates > 0) {
        playlistImportStatus.textContent = "Playlist is already up to date";
      } else {
        playlistImportStatus.textContent =
          "No indexed tracks matched this folder";
      }
      await loadPlaylists(playlist.id);
    } catch (err) {
      playlistImportStatus.textContent = err.message || "Folder import failed";
    } finally {
      importPlaylistFolderButton.disabled = false;
    }
  });
}

async function loadPlaylists(selectID = selectedPlaylistID) {
  setPlaylists(await apiModule.api("/api/playlists"));
  if (!playlists.some((playlist) => playlist.id === selectID)) {
    selectID = playlists[0]?.id || 0;
  }
  setSelectedPlaylistID(selectID);
  storageSet(playlistStorageKey, selectedPlaylistID || "");
  renderPlaylists();
  if (selectedPlaylistID) {
    await loadPlaylistDetail(selectedPlaylistID);
  } else {
    playlistDetailEl.replaceChildren(formatting.emptyHint("No playlists yet"));
  }
  searchModule.runSearch().catch(console.error);
}

function renderPlaylists() {
  playlistSelect.replaceChildren(
    ...playlists.map((playlist) => {
      const option = document.createElement("option");
      option.value = String(playlist.id);
      option.textContent = playlist.name;
      return option;
    }),
  );
  playlistSelect.hidden = playlists.length === 0;
  playlistSelect.value = selectedPlaylistID ? String(selectedPlaylistID) : "";
  updatePlaylistActionButtons();
}

async function loadPlaylistDetail(id) {
  const playlist = await apiModule.api(`/api/playlists/${id}`);
  renderPlaylistDetail(playlist);
}

function renderPlaylistItem(playlist, item) {
  const dedupeKey = item.dedupe_key || "";
  const track = {
    dedupe_key: dedupeKey,
    title: item.title || "Unknown track",
    artist: item.artist || "",
    album: item.album || "",
  };
  const extraButtons = [];
  if (playlist.can_edit) {
    const remove = trackUi.trashButton("Remove from playlist", async () => {
      const updated = await apiModule.api(
        `/api/playlists/${playlist.id}/items/${item.id}`,
        { method: "DELETE" },
      );
      renderPlaylistDetail(updated);
    });
    extraButtons.push(remove);
  }
  return trackUi.trackRow(
    track,
    trackUi.standardTrackCommands(dedupeKey),
    "",
    dedupeKey,
    extraButtons,
  );
}

function renderPlaylistDetail(playlist) {
  const items = playlist.items || [];
  const list = document.createElement("div");
  list.className = "playlist-items";
  list.replaceChildren(
    ...(items.length
      ? items.map((item) => renderPlaylistItem(playlist, item))
      : [formatting.emptyHint("No tracks in this playlist")]),
  );
  playlistDetailEl.replaceChildren(list);
  setPlaylists(
    playlists.map((existing) =>
      existing.id === playlist.id ? playlist : existing,
    ),
  );
  updatePlaylistActionButtons();
}

function updatePlaylistActionButtons() {
  const playlist = playlists.find((item) => item.id === selectedPlaylistID);
  deletePlaylistButton.hidden = !playlist?.can_edit;
  importPlaylistFolderButton.hidden = !playlist?.can_edit;
}

function setPlaylistButtonContent(button) {
  const icon = document.createElement("span");
  icon.className = "playlist-add-icon";
  icon.textContent = "+";
  const label = document.createElement("span");
  label.className = "playlist-add-label";
  label.textContent = "Playlist";
  button.replaceChildren(icon, label);
}

function closePlaylistAddMenus(except = null) {
  document.querySelectorAll(".playlist-add-menu").forEach((wrap) => {
    if (wrap === except) {
      return;
    }
    const menu = wrap.querySelector(".playlist-add-options");
    const button = wrap.querySelector("button");
    if (menu) {
      menu.hidden = true;
    }
    if (button) {
      button.setAttribute("aria-expanded", "false");
    }
  });
}

function restoreRailPreferences() {
  const storedPlaylistID = Number(storageGet(playlistStorageKey));
  setSelectedPlaylistID(
    Number.isInteger(storedPlaylistID) && storedPlaylistID > 0
      ? storedPlaylistID
      : 0,
  );
  const mode =
    storageGet(railModeStorageKey) === "playlists" ? "playlists" : "library";
  setRailMode(mode, { load: false, persist: false });
}

function setRailMode(mode, { load = true, persist = true } = {}) {
  const playlistsActive = mode === "playlists";
  const libraryActive = !playlistsActive;
  if (persist) {
    storageSet(railModeStorageKey, mode);
  }
  libraryTab.classList.toggle("active", libraryActive);
  playlistsTab.classList.toggle("active", playlistsActive);
  libraryViews.forEach((el) => {
    el.hidden = !libraryActive;
  });
  playlistsView.hidden = !playlistsActive;
  if (playlistsActive && load) {
    loadPlaylists(selectedPlaylistID).catch(console.error);
  }
}

async function loadLibraryStatus() {
  try {
    const info = await apiModule.api("/api/library");
    libraryStatus.textContent = `${info.track_count} tracks indexed`;
  } catch (err) {
    libraryStatus.textContent = "Library status unavailable";
    console.error(err);
  }
}

function getPlaylists() {
  return playlists;
}

function getSelectedPlaylistID() {
  return selectedPlaylistID;
}

export default {
  init,
  loadPlaylists,
  renderPlaylists,
  loadPlaylistDetail,
  renderPlaylistItem,
  renderPlaylistDetail,
  updatePlaylistActionButtons,
  setPlaylistButtonContent,
  closePlaylistAddMenus,
  restoreRailPreferences,
  setRailMode,
  loadLibraryStatus,
  getPlaylists,
  getSelectedPlaylistID,
};
