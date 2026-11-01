import { ipcRenderer } from "electron";
ipcRenderer.once("pondera-recorder-port", event => {
  window.postMessage("pondera-recorder-port", "*", event.ports);
});
