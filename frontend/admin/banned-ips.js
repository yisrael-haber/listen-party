import { configBannedIPs } from "./state.js";
import { renderListItem, updateListRemoveButtons } from "./list-editor.js";

export function renderBannedIPs(ips) {
  const rows = ips.length > 0 ? ips : [""];
  configBannedIPs.replaceChildren(
    ...rows.map((ip) =>
      renderListItem(
        ip,
        "banned-ip-input",
        "192.168.1.50",
        "Banned IP address",
      ),
    ),
  );
  updateListRemoveButtons(configBannedIPs);
}

export function addBannedIP(ip = "") {
  const row = renderListItem(
    ip,
    "banned-ip-input",
    "192.168.1.50",
    "Banned IP address",
  );
  configBannedIPs.append(row);
  updateListRemoveButtons(configBannedIPs);
  row.querySelector(".banned-ip-input").focus();
}

export function readBannedIPs() {
  return [...configBannedIPs.querySelectorAll(".banned-ip-input")]
    .map((input) => input.value.trim())
    .filter(Boolean);
}

export function init() {
  const addBannedIPButton = document.getElementById("addBannedIP");
  addBannedIPButton.addEventListener("click", () => {
    addBannedIP();
  });
}
