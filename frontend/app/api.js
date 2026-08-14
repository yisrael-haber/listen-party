import { roomAPI } from "./state.js";
import renderStateModule from "./render-state.js";

async function api(path, options = {}) {
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...options,
  });
  if (!res.ok) {
    throw new Error(await res.text());
  }
  if (res.status === 204) {
    return null;
  }
  const body = await res.text();
  if (!body) {
    return null;
  }
  return JSON.parse(body);
}

async function command(body) {
  const state = await api(roomAPI("/api/command"), {
    method: "POST",
    body: JSON.stringify(body),
  });
  renderStateModule.renderState(state);
}

export default { api, command };
