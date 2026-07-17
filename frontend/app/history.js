import formatting from "./formatting.js";
import trackUi from "./track-ui.js";

const historyEl = document.getElementById("history");

function renderHistoryItem(item) {
	const track = item.track;
	const dedupeKey = item.dedupe_key;
	return trackUi.trackRow(track || {title: "Unavailable track", dedupe_key: dedupeKey}, trackUi.standardTrackCommands(dedupeKey), formatting.playbackRequester(item), dedupeKey, [], true);
}

function renderHistory(history) {
  if (history.length === 0) {
    const empty = document.createElement("p");
    empty.className = "hint";
    empty.textContent = "No previously played tracks";
    historyEl.replaceChildren(empty);
    return;
  }
  historyEl.replaceChildren(...history.map(renderHistoryItem));
}

export default { renderHistoryItem, renderHistory };
