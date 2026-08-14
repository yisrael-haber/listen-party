import {
  albumSearchStorageKey,
  albumStorageKey,
  albumSearchTimer,
  setAlbumSearchTimer,
  searchDebounceMS,
  storageGet,
  storageSet,
} from "./state.js";
import formatting from "./formatting.js";
import trackUi from "./track-ui.js";
import permissions from "./permissions.js";
import apiModule from "./api.js";

let albumSearchInput,
  albumsToolbar,
  albumListEl,
  albumDetailEl,
  albumDetailHeadEl,
  albumDetailCopyEl,
  albumDetailActionsEl,
  albumBackButton,
  albumStatus;

let openAlbumKey = "";

function init() {
  albumSearchInput = document.getElementById("albumSearch");
  albumsToolbar = document.getElementById("albumsToolbar");
  albumListEl = document.getElementById("albumList");
  albumDetailEl = document.getElementById("albumDetail");
  albumDetailHeadEl = document.getElementById("albumDetailHead");
  albumDetailCopyEl = document.getElementById("albumDetailCopy");
  albumDetailActionsEl = document.getElementById("albumDetailActions");
  albumBackButton = document.getElementById("closeAlbumDetail");
  albumStatus = document.getElementById("albumStatus");

  albumSearchInput.addEventListener("input", () => {
    storageSet(albumSearchStorageKey, albumSearchInput.value);
    clearTimeout(albumSearchTimer);
    setAlbumSearchTimer(
      setTimeout(() => {
        loadAlbums().catch(console.error);
      }, searchDebounceMS),
    );
  });

  albumBackButton.addEventListener("click", () => {
    closeAlbum();
  });
}

async function loadAlbums() {
  if (openAlbumKey) {
    await openAlbum(openAlbumKey, { refresh: true });
    return;
  }
  const q = albumSearchInput.value.trim();
  const albums = await apiModule.api(`/api/albums?${new URLSearchParams({ q })}`);
  if (q !== albumSearchInput.value.trim()) {
    return;
  }
  renderAlbumList(albums || []);
}

function renderAlbumList(albums) {
  albumStatus.textContent = albums.length
    ? `${albums.length} album${albums.length === 1 ? "" : "s"}`
    : "";
  albumListEl.replaceChildren(
    ...(albums.length
      ? albums.map(albumRow)
      : [formatting.emptyHint("No albums found")]),
  );
}

function albumRow(album) {
  const row = document.createElement("div");
  row.className = "item album-item";

  const open = document.createElement("button");
  open.type = "button";
  open.className = "album-open";
  open.append(
    trackUi.trackMeta(
      album.name || "Unknown album",
      [
        album.artist,
        `${album.track_count} track${album.track_count === 1 ? "" : "s"}`,
      ]
        .filter(Boolean)
        .join(" · "),
    ),
  );
  open.addEventListener("click", () => {
    openAlbum(album.key).catch(console.error);
  });

  row.append(open, albumCommands(album));
  return row;
}

function albumCommandButtons(album) {
  return [
    trackUi.commandButton("Queue", {
      action: "queue_album",
      album_key: album.key,
    }),
    trackUi.commandButton("Play", {
      action: "play_album",
      album_key: album.key,
    }),
  ];
}

function albumCommands(album) {
  const actions = document.createElement("div");
  actions.className = "row-actions";
  actions.append(...albumCommandButtons(album));
  permissions.updateRowActionLayout(actions);
  return actions;
}

async function openAlbum(key, { refresh = false } = {}) {
  const album = await apiModule.api(`/api/albums/${encodeURIComponent(key)}`);
  openAlbumKey = key;
  if (!refresh) {
    storageSet(albumStorageKey, key);
  }
  renderAlbumDetail(album);
}

function renderAlbumDetail(album) {
  const title = document.createElement("h3");
  title.className = "album-detail-title";
  title.textContent = album.name || "Unknown album";
  const sub = document.createElement("p");
  sub.className = "hint";
  sub.textContent = [
    album.artist,
    `${album.track_count} track${album.track_count === 1 ? "" : "s"}`,
  ]
    .filter(Boolean)
    .join(" · ");
  albumDetailCopyEl.replaceChildren(title, sub);
  albumDetailActionsEl.replaceChildren(...albumCommandButtons(album));
  permissions.updateRowActionLayout(albumDetailActionsEl);

  const tracks = album.tracks || [];
  albumDetailEl.replaceChildren(
    ...(tracks.length
      ? tracks.map((track) =>
          trackUi.trackRow(
            track,
            trackUi.standardTrackCommands(track.dedupe_key),
            "",
            track.dedupe_key,
            [],
            true,
          ),
        )
      : [formatting.emptyHint("No playable tracks in this album")]),
  );
  albumStatus.textContent = "";
  setDetailVisible(true);
}

function closeAlbum() {
  openAlbumKey = "";
  storageSet(albumStorageKey, "");
  setDetailVisible(false);
  loadAlbums().catch(console.error);
}

function setDetailVisible(visible) {
  albumsToolbar.hidden = visible;
  albumListEl.hidden = visible;
  albumDetailEl.hidden = !visible;
  albumDetailHeadEl.hidden = !visible;
}

function restoreAlbumPreferences() {
  albumSearchInput.value = storageGet(albumSearchStorageKey);
  const key = storageGet(albumStorageKey);
  if (key) {
    openAlbum(key).catch(() => {
      storageSet(albumStorageKey, "");
      openAlbumKey = "";
      setDetailVisible(false);
    });
  }
}

export default { init, loadAlbums, restoreAlbumPreferences };
