import { configMusicDirs } from "./state.js";
import { renderListItem, updateListRemoveButtons } from "./list-editor.js";
import { rescanMusicDir } from "./scan.js";

export function renderMusicDirs(paths) {
  const rows = paths.length > 0 ? paths : [""];
  configMusicDirs.replaceChildren(...rows.map(renderMusicDirItem));
  updateListRemoveButtons(configMusicDirs);
}

export function renderMusicDirItem(path) {
  const row = renderListItem(
    path,
    "music-dir-input",
    "/path/to/music",
    "Music directory",
  );
  row.classList.add("music-dir-item");
  const rescan = document.createElement("button");
  rescan.className = "secondary compact path-rescan";
  rescan.type = "button";
  rescan.textContent = "Rescan";
  rescan.addEventListener("click", async () => {
    await rescanMusicDir(
      row.querySelector(".music-dir-input").value.trim(),
      rescan,
    );
  });
  row.insertBefore(rescan, row.lastElementChild);
  return row;
}

export function addMusicDir(path = "") {
  const row = renderMusicDirItem(path);
  configMusicDirs.append(row);
  updateListRemoveButtons(configMusicDirs);
  row.querySelector(".music-dir-input").focus();
}

export function readMusicDirs() {
  return [...configMusicDirs.querySelectorAll(".music-dir-input")]
    .map((input) => input.value.trim())
    .filter(Boolean);
}

export function init() {
  const addMusicDirButton = document.getElementById("addMusicDir");
  addMusicDirButton.addEventListener("click", () => {
    addMusicDir();
  });
}
