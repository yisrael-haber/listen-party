import {
  searchTextStorageKey,
  searchFieldStorageKey,
  searchTimer,
  setSearchTimer,
  searchDebounceMS,
  storageGet,
  storageSet,
} from "./state.js";
import formatting from "./formatting.js";
import trackUi from "./track-ui.js";
import apiModule from "./api.js";

let searchInput, searchField, resultsEl;

function init() {
  searchInput = document.getElementById("q");
  searchField = document.getElementById("searchField");
  resultsEl = document.getElementById("results");

  document
    .getElementById("searchForm")
    .addEventListener("submit", async (event) => {
      event.preventDefault();
      await runSearch();
    });

  searchInput.addEventListener("input", () => {
    storageSet(searchTextStorageKey, searchInput.value);
    clearTimeout(searchTimer);
    resultsEl.replaceChildren();
    setSearchTimer(
      setTimeout(() => {
        runSearch().catch(console.error);
      }, searchDebounceMS),
    );
  });

  searchField.addEventListener("change", () => {
    storageSet(searchFieldStorageKey, searchField.value);
    clearTimeout(searchTimer);
    runSearch().catch(console.error);
  });
}

async function runSearch() {
  const q = searchInput.value.trim();
  const field = searchField.value;
  const params = new URLSearchParams({ q, field });
  const tracks = await apiModule.api(`/api/search?${params}`);
  if (q !== searchInput.value.trim() || field !== searchField.value) {
    return;
  }
  resultsEl.replaceChildren(
    ...(tracks?.length
      ? tracks.map((track) =>
          trackUi.trackRow(
            track,
            trackUi.standardTrackCommands(track.dedupe_key),
          ),
        )
      : [formatting.emptyHint("No matching tracks")]),
  );
}

function restoreSearchPreferences() {
  searchInput.value = storageGet(searchTextStorageKey);
  const field = storageGet(searchFieldStorageKey);
  if ([...searchField.options].some((option) => option.value === field)) {
    searchField.value = field;
  }
}

export default { init, runSearch, restoreSearchPreferences };
