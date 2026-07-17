import { queueSortable, queueDragActive, queueReorderPending, pendingQueueState, roomAPI, setQueueSortable, setQueueDragActive, setQueueReorderPending, setPendingQueueState } from "./state.js";
import formatting from "./formatting.js";
import trackUi from "./track-ui.js";
import permissions from "./permissions.js";
import apiModule from "./api.js";
import renderStateModule from "./render-state.js";

let queueEl, clearQueueButton, clearHistoryButton, queueChangesButton, queueChangesListEl;

function init() {
  queueEl = document.getElementById("queue");
  clearQueueButton = document.getElementById("clearQueue");
  clearHistoryButton = document.getElementById("clearHistory");
  queueChangesButton = document.getElementById("queueChangesButton");
  queueChangesListEl = document.getElementById("queueChangesList");

  clearQueueButton.addEventListener("click", async () => {
    await apiModule.command({action: "queue_clear"});
  });

  clearHistoryButton.addEventListener("click", async () => {
    await apiModule.command({action: "history_clear"});
  });

  queueChangesButton.addEventListener("click", () => {
    const nextOpen = queueChangesListEl.hidden;
    queueChangesListEl.hidden = !nextOpen;
    queueChangesButton.setAttribute("aria-expanded", String(nextOpen));
  });

  initQueueSortable();
}

function renderQueueItem(item) {
  const li = document.createElement("li");
  li.className = "item queue-item";
  li.dataset.queueItemId = String(item.id);

  const track = item.track;
  const meta = trackUi.trackMeta(
    track ? formatting.trackTitle(track) : "Unavailable track",
    track ? formatting.trackSubtitleWithDuration(track) : "",
    formatting.playbackRequester(item)
  );

  const actions = trackUi.trackActionGroup([], item.dedupe_key, [
    trackUi.commandTrashButton("Remove from queue", {action: "queue_remove", queue_item_id: item.id}),
  ]);

	if (permissions.hasRoomPermission("queue_manage")) {
		li.classList.add("queue-item-draggable");
		li.append(queueDragHandle(item), meta, actions);
	} else {
		li.append(meta, actions);
	}
  return li;
}

function queueDragHandle(item) {
	const handle = document.createElement("button");
	handle.className = "queue-drag-handle";
	handle.type = "button";
	handle.title = "Drag to reorder";
	handle.setAttribute("aria-label", `Reorder ${item.track ? formatting.trackTitle(item.track) : "unavailable track"}`);
	const icon = document.createElement("span");
	icon.className = "queue-drag-icon";
	icon.setAttribute("aria-hidden", "true");
	handle.append(icon);
	handle.addEventListener("keydown", (event) => {
		handleQueueReorderKey(event, item.id);
	});
	return handle;
}

function handleQueueReorderKey(event, queueItemID) {
	if (!["ArrowUp", "ArrowDown", "Home", "End"].includes(event.key) || queueReorderPending) return;
	const item = event.currentTarget.closest(".queue-item");
	if (!item) return;
	let before = null;
	if (event.key === "ArrowUp") {
		before = item.previousElementSibling;
		if (!before) return;
	} else if (event.key === "ArrowDown") {
		const next = item.nextElementSibling;
		if (!next) return;
		before = next.nextElementSibling;
	} else if (event.key === "Home") {
		before = queueEl.firstElementChild;
		if (before === item) return;
	}
	event.preventDefault();
	const beforeQueueItemID = before ? Number(before.dataset.queueItemId) : 0;
	submitQueueReorder(queueItemID, beforeQueueItemID).then(() => {
		queueEl.querySelector(`[data-queue-item-id="${queueItemID}"] .queue-drag-handle`)?.focus();
	});
}

function renderQueueChanges(actions) {
  queueChangesButton.dataset.empty = String(actions.length === 0);
  queueChangesButton.textContent = actions.length ? `Queue changes ${actions.length}` : "Queue changes";
  queueChangesListEl.replaceChildren(...actions.map((action) => {
    const item = document.createElement("div");
    item.className = "queue-change-item";

    const meta = document.createElement("div");
    meta.className = "queue-change-meta";
    const metadata = [
      [formatting.formatActionTime(action.at), "queue-change-time"],
      [action.ip, "queue-change-ip"],
      [action.username, "queue-change-username"],
    ];
    for (const [value, className] of metadata) {
      if (!value) continue;
      const field = document.createElement("span");
      field.className = className;
      field.textContent = value;
      meta.append(field);
    }

    const text = document.createElement("div");
    text.className = "queue-change-text";
    text.textContent = action.text || "";

    item.append(meta, text);
    return item;
  }));
  if (actions.length === 0) {
    const empty = document.createElement("div");
    empty.className = "queue-change-empty";
    empty.textContent = "No queue changes yet";
    queueChangesListEl.append(empty);
  }
}

function initQueueSortable() {
	if (typeof Sortable === "undefined") {
		throw new Error("embedded SortableJS asset did not load");
	}
	const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
	setQueueSortable(Sortable.create(queueEl, {
		animation: reduceMotion ? 0 : 160,
		easing: "cubic-bezier(0.2, 0, 0, 1)",
		handle: ".queue-drag-handle",
		draggable: ".queue-item",
		dataIdAttr: "data-queue-item-id",
		ghostClass: "queue-sortable-ghost",
		chosenClass: "queue-sortable-chosen",
		dragClass: "queue-sortable-drag",
		forceFallback: true,
		fallbackOnBody: true,
		fallbackTolerance: 4,
		delay: 120,
		delayOnTouchOnly: true,
		touchStartThreshold: 4,
		onStart() {
			setQueueDragActive(true);
			setPendingQueueState(null);
			queueEl.classList.add("queue-dragging");
		},
		onEnd(event) {
			setQueueDragActive(false);
			queueEl.classList.remove("queue-dragging");
			if (event.oldDraggableIndex === event.newDraggableIndex) {
				applyPendingQueueState();
				return;
			}
			const queueItemID = Number(event.item.dataset.queueItemId);
			const before = event.item.nextElementSibling;
			const beforeQueueItemID = before ? Number(before.dataset.queueItemId) : 0;
			submitQueueReorder(queueItemID, beforeQueueItemID);
		},
	}));
	updateQueueSortable();
}

function updateQueueSortable() {
	if (!queueSortable) return;
	const enabled = permissions.hasRoomPermission("queue_manage") && !queueReorderPending;
	queueSortable.option("disabled", !enabled);
	queueEl.classList.toggle("queue-sortable-enabled", enabled);
}

function applyPendingQueueState() {
	const state = pendingQueueState;
	setPendingQueueState(null);
	if (state) renderStateModule.renderState(state);
}

async function submitQueueReorder(queueItemID, beforeQueueItemID) {
	if (queueReorderPending || !permissions.hasRoomPermission("queue_manage")) return;
	setQueueReorderPending(true);
	updateQueueSortable();
	try {
		const state = await apiModule.api(roomAPI("/api/command"), {
			method: "POST",
			body: JSON.stringify({
				action: "queue_reorder",
				queue_item_id: queueItemID,
				before_queue_item_id: beforeQueueItemID,
			}),
		});
		setQueueReorderPending(false);
		renderStateModule.renderState(state);
		applyPendingQueueState();
	} catch (err) {
		console.error(err);
		setQueueReorderPending(false);
		setPendingQueueState(null);
		try {
			renderStateModule.renderState(await apiModule.api(roomAPI("/api/state")));
		} catch (refreshErr) {
			console.error(refreshErr);
		}
		queueEl.classList.add("queue-reorder-error");
		setTimeout(() => queueEl.classList.remove("queue-reorder-error"), 500);
	} finally {
		updateQueueSortable();
	}
}

export default { init, renderQueueItem, queueDragHandle, handleQueueReorderKey, renderQueueChanges, initQueueSortable, updateQueueSortable, applyPendingQueueState, submitQueueReorder, formatActionTime: formatting.formatActionTime };
