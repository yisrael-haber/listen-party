export const configStatus = document.getElementById("configStatus");
export const configForm = document.getElementById("configForm");
export const configSaveButton = document.getElementById("configSave");
export const configAddr = document.getElementById("configAddr");
export const configMusicDirs = document.getElementById("configMusicDirs");
export const configBannedIPs = document.getElementById("configBannedIPs");
export const configScanWorkers = document.getElementById("configScanWorkers");
export const configKeycloakEnabled = document.getElementById(
  "configKeycloakEnabled",
);
export const configKeycloakIssuer = document.getElementById(
  "configKeycloakIssuer",
);
export const configKeycloakClientID = document.getElementById(
  "configKeycloakClientID",
);
export const configKeycloakClientSecret = document.getElementById(
  "configKeycloakClientSecret",
);
export const configKeycloakDisplayName = document.getElementById(
  "configKeycloakDisplayName",
);
export const addMusicDirButton = document.getElementById("addMusicDir");
export const addBannedIPButton = document.getElementById("addBannedIP");
export const addRoomButton = document.getElementById("addRoom");
export const roomsList = document.getElementById("roomsList");
export const rescanButton = document.getElementById("rescan");
export const rescanStatus = document.getElementById("rescanStatus");
export const scanStatus = document.getElementById("scanStatus");

export const minimumSaveFeedbackMS = 350;

let scanStatusTimer = 0;
let saveFeedbackTimer = 0;
let roomCounter = 1;
let configRevision = 0;

export { scanStatusTimer, saveFeedbackTimer, roomCounter, configRevision };

export function setScanStatusTimer(value) {
  scanStatusTimer = value;
}

export function setSaveFeedbackTimer(value) {
  saveFeedbackTimer = value;
}

export function setRoomCounter(value) {
  roomCounter = value;
}

export function setConfigRevision(value) {
  configRevision = value;
}
