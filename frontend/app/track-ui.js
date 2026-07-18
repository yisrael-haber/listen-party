import formatting from "./formatting.js";
import permissions from "./permissions.js";
import apiModule from "./api.js";
import playlists from "./playlists.js";

function trackMeta(titleText, subtitleText, requestedBy = "") {
  const meta = document.createElement("div");
  meta.className = "meta";

  const title = document.createElement("div");
  title.className = "title";
  title.textContent = titleText;

  const sub = document.createElement("div");
  sub.className = "sub";
  renderSubtitle(sub, subtitleText, requestedBy);

  meta.append(title, sub);
  return meta;
}

function renderSubtitle(element, subtitleText, requestedBy = "") {
  element.replaceChildren();
  if (subtitleText) {
    const context = document.createElement("span");
    context.className = "track-context";
    context.textContent = subtitleText;
    element.append(context);
  }
  if (!requestedBy) {
    return;
  }
  if (subtitleText) {
    element.append(document.createTextNode(" - Queued by "));
  } else {
    element.append(document.createTextNode("Queued by "));
  }
  const requester = document.createElement("span");
  requester.className = "requester";
  requester.textContent = requestedBy;
  element.append(requester);
}

function commandButton(text, body) {
  const button = document.createElement("button");
  button.className = "secondary compact row-command-button";
  button.title = text;
  button.setAttribute("aria-label", text);
  button.dataset.roomAction = body.action;
  const icon = commandIcon(body.action);
  if (icon) {
    const iconEl = document.createElement("span");
    iconEl.className = "command-icon";
    iconEl.setAttribute("aria-hidden", "true");
    iconEl.textContent = icon;
    button.append(iconEl);
  }
  const label = document.createElement("span");
  label.className = "command-label";
  label.textContent = text;
  button.append(label);
  button.hidden = !permissions.canRunCommand(body.action);
  button.addEventListener("click", async (event) => {
    await apiModule.command(body);
    button.blur();
  });
  return button;
}

function commandIcon(action) {
  if (action === "queue_add") {
    return "≡+";
  }
  if (action === "play_now" || action === "play") {
    return "▶";
  }
  return "";
}

function commandTrashButton(label, body) {
  const button = trashButton(label, async () => {
    await apiModule.command(body);
  });
  button.dataset.roomAction = body.action;
  button.hidden = !permissions.canRunCommand(body.action);
  return button;
}

function trashButton(label, onClick) {
  const button = document.createElement("button");
  button.className = "secondary compact icon-only trash-button";
  button.type = "button";
  button.title = label;
  button.setAttribute("aria-label", label);
  button.append(document.createElement("span"));
  button.addEventListener("click", onClick);
  return button;
}

function standardTrackCommands(dedupeKey) {
  if (!dedupeKey) {
    return [];
  }
  return [
    ["Queue", { action: "queue_add", dedupe_key: dedupeKey }],
    ["Play", { action: "play_now", dedupe_key: dedupeKey }],
  ];
}

function trackActionGroup(commandSpecs, dedupeKey, extraButtons = []) {
  const actions = document.createElement("div");
  actions.className = "row-actions";
  actions.append(
    ...commandSpecs.map(([text, body]) => commandButton(text, body)),
  );
  if (dedupeKey) {
    actions.append(addToPlaylistButton(dedupeKey));
  }
  actions.append(...extraButtons);
  permissions.updateRowActionLayout(actions);
  return actions;
}

function addToPlaylistButton(dedupeKey) {
  const editable = playlists
    .getPlaylists()
    .filter((playlist) => playlist.can_edit);
  const wrap = document.createElement("div");
  wrap.className = "playlist-add-menu";
  const button = document.createElement("button");
  button.className = "secondary compact playlist-more-button";
  button.type = "button";
  playlists.setPlaylistButtonContent(button);
  button.title = "Add to playlist";
  button.setAttribute("aria-label", "Add to playlist");
  if (editable.length === 0) {
    button.disabled = true;
    wrap.append(button);
    return wrap;
  }
  button.setAttribute("aria-haspopup", "menu");
  button.setAttribute("aria-expanded", "false");
  const menu = document.createElement("div");
  menu.className = "playlist-add-options";
  menu.hidden = true;
  for (const playlist of editable) {
    const item = document.createElement("button");
    item.type = "button";
    item.className = "playlist-add-option";
    item.textContent = playlist.name;
    item.addEventListener("click", async () => {
      menu.hidden = true;
      button.setAttribute("aria-expanded", "false");
      await apiModule.api(`/api/playlists/${playlist.id}/items`, {
        method: "POST",
        body: JSON.stringify({ dedupe_key: dedupeKey }),
      });
      await playlists.loadPlaylists(playlist.id);
    });
    menu.append(item);
  }
  button.addEventListener("click", (event) => {
    event.stopPropagation();
    playlists.closePlaylistAddMenus(wrap);
    const open = menu.hidden;
    menu.hidden = !open;
    button.setAttribute("aria-expanded", String(open));
  });
  wrap.append(button, menu);
  return wrap;
}

function trackRow(
  track,
  commandSpecs,
  requestedBy = "",
  dedupeKey = track?.dedupe_key || "",
  extraButtons = [],
  showDuration = false,
) {
  const row = document.createElement("div");
  row.className = "item";

  const subtitle = showDuration
    ? formatting.trackSubtitleWithDuration(track)
    : formatting.trackSubtitle(track);
  const meta = trackMeta(formatting.trackTitle(track), subtitle, requestedBy);
  const actionEl = trackActionGroup(commandSpecs, dedupeKey, extraButtons);

  row.append(meta, actionEl);
  return row;
}

export default {
  trackMeta,
  renderSubtitle,
  commandButton,
  commandIcon,
  commandTrashButton,
  trashButton,
  standardTrackCommands,
  trackActionGroup,
  trackRow,
  addToPlaylistButton,
};
