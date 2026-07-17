function formatTime(seconds) {
  if (!Number.isFinite(seconds) || seconds < 0) seconds = 0;
  const total = Math.floor(seconds);
  const minutes = Math.floor(total / 60);
  const rest = String(total % 60).padStart(2, "0");
  return `${minutes}:${rest}`;
}

function trackTitle(track) {
  if (!track) return "";
  return (track.title || `Track ${track.id || ""}`).trim();
}

function trackContext(track) {
  if (!track) return "";
  return [track.artist, track.album].filter(Boolean).join(" · ");
}

function trackSubtitle(track) {
  return [trackContext(track), track?.track_no ? `Track ${track.track_no}` : ""].filter(Boolean).join(" · ");
}

function trackSubtitleWithDuration(track) {
  const duration = track?.duration_ms > 0 ? formatTime(track.duration_ms / 1000) : "";
  return [trackSubtitle(track), duration].filter(Boolean).join(" · ");
}

function playbackRequester(item) {
  return item?.source === "auto_dj" ? "Auto-DJ" : (item?.requested_by || "");
}

function emptyHint(text, tag = "p") {
  const hint = document.createElement(tag);
  hint.className = "hint empty-state";
  hint.textContent = text;
  return hint;
}

function formatActionTime(value) {
  const time = Date.parse(value || "");
  if (!Number.isFinite(time)) return "";
  return new Intl.DateTimeFormat(undefined, {hour: "2-digit", minute: "2-digit"}).format(new Date(time));
}

export default { formatTime, trackTitle, trackContext, trackSubtitle, trackSubtitleWithDuration, playbackRequester, emptyHint, formatActionTime };
