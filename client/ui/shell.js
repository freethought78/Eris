import { attachManualUI } from "./manual.js";
import { attachRoomUI } from "./room.js";
import { attachChatUI } from "./chat.js";
import { setupRenegotiation } from "../webrtc/peer.js";
import { getStream, stopStream, addMediaToPC, removeMediaFromPC, createVideoElement } from "../channels/media.js";
import { getScreenStream } from "../channels/screen.js";
import { initTileGrid, addTile, removeTile, hasTile } from "./tiles.js";
import { initCall, requestCall, cancelCall, endCall, getCallState, resetCallState, setPeerName } from "./call.js";

const app = document.getElementById("app");
if (!app) throw new Error("#app element not found");

app.innerHTML = `
  <div id="layout">
    <header id="topbar">
      <button id="menu-btn" aria-label="Menu">&#9776;</button>
      <h1>Eris</h1>
    </header>
    <div id="body">
      <aside id="sidebar">
        <ul id="peer-list"></ul>
      </aside>
      <main id="content">
        <p id="welcome">Welcome to Eris. Open the menu to connect.</p>
      </main>
    </div>

    <footer id="toolbar">
      <button id="btn-mic" class="toolbar-btn" disabled title="Microphone">🎤</button>
      <button id="btn-cam" class="toolbar-btn" disabled title="Camera">📷</button>
      <button id="btn-screen" class="toolbar-btn" disabled title="Share screen">📺</button>
      <button id="btn-file" class="toolbar-btn" disabled title="Send file">📎</button>
      <input type="file" id="chat-file-picker" style="display:none">
    </footer>
  </div>

  <nav id="menu-panel">
    <button id="menu-connect" class="menu-item">Connect</button>
    <hr>
    <button id="menu-about" class="menu-item">About</button>
  </nav>

  <div id="modal-overlay" style="display:none">
    <div id="modal">
      <header>
        <h2>Connect</h2>
        <button id="modal-close" aria-label="Close">&times;</button>
      </header>
      <div id="modal-body">
        <div id="modal-tabs">
          <button class="tab active" data-mode="manual">Manual</button>
          <button class="tab" data-mode="room">Room</button>
        </div>
        <div id="modal-content"></div>
      </div>
    </div>
  </div>
`;

// ── Menu ──

const menuBtn = document.getElementById("menu-btn");
const menuPanel = document.getElementById("menu-panel");
let menuOpen = false;

menuBtn.addEventListener("click", () => {
  menuOpen = !menuOpen;
  menuPanel.classList.toggle("open", menuOpen);
});

document.addEventListener("click", (e) => {
  if (menuOpen && !menuPanel.contains(e.target) && e.target !== menuBtn) {
    menuOpen = false;
    menuPanel.classList.remove("open");
  }
});

// ── Modal ──

const overlay = document.getElementById("modal-overlay");
const modalClose = document.getElementById("modal-close");
const modalContent = document.getElementById("modal-content");
const tabs = document.querySelectorAll("#modal-tabs .tab");

function showMode(mode) {
  tabs.forEach((t) => t.classList.toggle("active", t.dataset.mode === mode));
  modalContent.innerHTML = "";
  if (mode === "manual") attachManualUI(modalContent);
  else if (mode === "room") attachRoomUI(modalContent);
}

tabs.forEach((tab) => {
  tab.addEventListener("click", () => showMode(tab.dataset.mode));
});

document.getElementById("menu-connect").addEventListener("click", () => {
  menuOpen = false;
  menuPanel.classList.remove("open");
  overlay.style.display = "flex";
  showMode("manual");
});

modalClose.addEventListener("click", () => {
  overlay.style.display = "none";
});

overlay.addEventListener("click", (e) => {
  if (e.target === overlay) overlay.style.display = "none";
});

// ── Connected state ──

let activePC = null;
let activeSignalDC = null;
let localStream = null;
let screenStream = null;
let pendingMediaKind = null;

document.addEventListener("eris-connected", (e) => {
  const { pc, chat, file, signal, displayName } = e.detail;
  activePC = pc;
  activeSignalDC = signal;
  localStream = null;
  screenStream = null;
  pendingMediaKind = null;

  overlay.style.display = "none";

  // Replace #body with split pane layout
  const body = document.getElementById("body");
  body.innerHTML = `
    <div id="split-container">
      <div id="top-panel">
        <div id="video-grid"></div>
      </div>
      <div id="divider"></div>
      <div id="bottom-panel"></div>
    </div>
  `;

  initTileGrid(document.getElementById("video-grid"));

  // Chat goes in bottom panel
  attachChatUI(document.getElementById("bottom-panel"), { dc: chat, dcFile: file, displayName });

  // Draggable divider
  const divider = document.getElementById("divider");
  const topPanel = document.getElementById("top-panel");
  const bottomPanel = document.getElementById("bottom-panel");
  let dragging = false;

  divider.addEventListener("mousedown", (e) => {
    dragging = true;
    divider.classList.add("active");
    document.body.style.cursor = "row-resize";
    document.body.style.userSelect = "none";
  });

  document.addEventListener("mousemove", (e) => {
    if (!dragging) return;
    const splitContainer = document.getElementById("split-container");
    const rect = splitContainer.getBoundingClientRect();
    const pct = ((e.clientY - rect.top) / rect.height) * 100;
    const clamped = Math.max(10, Math.min(90, pct));
    topPanel.style.flex = clamped;
    bottomPanel.style.flex = 100 - clamped;
  });

  document.addEventListener("mouseup", () => {
    if (dragging) {
      dragging = false;
      divider.classList.remove("active");
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    }
  });

  // Renegotiation
  const trackTileMap = new Map();
  setupRenegotiation(pc, signal, {
    onRemoteTrack: (stream) => {
      const isScreen = pendingMediaKind === "screen";
      const tileId = isScreen ? "remote-screen" : "remote-cam";
      if (isScreen) pendingMediaKind = null;
      addTile(tileId, isScreen ? "Screen" : "Peer", stream, false);
      for (const t of stream.getTracks()) trackTileMap.set(t, tileId);
    },
    onRemoteTrackEnded: (track) => {
      const tileId = trackTileMap.get(track);
      if (tileId) { removeTile(tileId); trackTileMap.delete(track); }
    },
  });

  // Peer name exchange
  signal.send(JSON.stringify({ type: "peer-info", name: displayName }));

  // Call flow
  initCall(signal, "Peer", {
    onCallAccepted: async () => {
      try {
        if (!localStream) localStream = await getStream({ audio: true, video: false });
        else {
          const s = await getStream({ audio: true, video: false });
          for (const t of s.getAudioTracks()) localStream.addTrack(t);
        }
        addMediaToPC(activePC, localStream);
        btnMic.classList.add("active");
        if (localStream.getVideoTracks().length && !hasTile("local-cam")) {
          addTile("local-cam", "You", localStream, true);
        }
      } catch (err) {
        alert("Failed to start mic: " + err.message);
      }
    },
    onCallEnded: () => {
      if (localStream?.getAudioTracks().length) {
        removeMediaFromPC(activePC, localStream, "audio");
        if (!localStream.getTracks().length) {
          stopStream(localStream);
          localStream = null;
          removeTile("local-cam");
        }
      }
      btnMic.classList.remove("active");
    },
  });

  signal.addEventListener("message", (e) => {
    let msg;
    try { msg = JSON.parse(e.data); } catch { return; }
    if (msg.type === "peer-info") setPeerName(msg.name);
    else if (msg.type === "media-kind") pendingMediaKind = msg.kind;
    else if (msg.type === "media-ended" && msg.kind === "screen") {
      removeTile("remote-screen");
    }
  });

  document.getElementById("btn-mic").disabled = false;
  document.getElementById("btn-cam").disabled = false;
  document.getElementById("btn-screen").disabled = false;
  document.getElementById("btn-file").disabled = false;
});

// ── Media controls ──

const btnMic = document.getElementById("btn-mic");
const btnCam = document.getElementById("btn-cam");
const btnScreen = document.getElementById("btn-screen");
const btnFile = document.getElementById("btn-file");

btnMic.addEventListener("click", toggleMic);
btnCam.addEventListener("click", toggleCam);
btnScreen.addEventListener("click", toggleScreen);
btnFile.addEventListener("click", () => document.getElementById("chat-file-picker")?.click());

async function toggleMic() {
  if (!activePC) return;
  const s = getCallState();
  if (s === "idle") {
    requestCall("audio");
  } else if (s === "calling") {
    cancelCall();
    btnMic.classList.remove("active");
  } else if (s === "connected") {
    endCall("audio");
    btnMic.classList.remove("active");
  }
  // s === "ringing" — overlay handles it; button does nothing
}

async function toggleCam() {
  if (!activePC) return;
  if (localStream?.getVideoTracks().length) {
    removeMediaFromPC(activePC, localStream, "video");
    if (!localStream.getTracks().length) {
      stopStream(localStream);
      localStream = null;
    }
    removeTile("local-cam");
    btnCam.classList.remove("active");
    return;
  }
  try {
    if (!localStream) localStream = await getStream({ audio: true, video: true });
    else {
      const s = await getStream({ audio: false, video: true });
      for (const t of s.getVideoTracks()) localStream.addTrack(t);
    }
    addMediaToPC(activePC, localStream);
    addTile("local-cam", "You", localStream, true);
    btnCam.classList.add("active");
  } catch (err) {
    alert(err.message);
  }
}

async function toggleScreen() {
  if (!activePC) return;
  if (screenStream) {
    activeSignalDC?.send(JSON.stringify({ type: "media-ended", kind: "screen" }));
    removeMediaFromPC(activePC, screenStream);
    stopStream(screenStream);
    screenStream = null;
    btnScreen.classList.remove("active");
    removeTile("local-screen");
    return;
  }
  try {
    const stream = await getScreenStream();
    if (!stream) return;
    screenStream = stream;

    stream.getVideoTracks()[0]?.addEventListener("ended", () => {
      if (!screenStream) return;
      activeSignalDC?.send(JSON.stringify({ type: "media-ended", kind: "screen" }));
      removeMediaFromPC(activePC, screenStream);
      stopStream(screenStream);
      screenStream = null;
      btnScreen.classList.remove("active");
      removeTile("local-screen");
    });

    addMediaToPC(activePC, screenStream);
    activeSignalDC?.send(JSON.stringify({ type: "media-kind", kind: "screen" }));
    addTile("local-screen", "Screen", screenStream, true);
    btnScreen.classList.add("active");
  } catch (err) {
    if (err.name !== "AbortError") alert(err.message);
  }
}
