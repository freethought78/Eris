import { attachManualUI } from "./manual.js";
import { attachRoomUI } from "./room.js";
import { attachChatUI } from "./chat.js";
import { setupRenegotiation } from "../webrtc/peer.js";
import { getStream, stopStream, addMediaToPC, removeMediaFromPC, createVideoElement } from "../channels/media.js";
import { getScreenStream } from "../channels/screen.js";

const app = document.getElementById("app");
if (!app) throw new Error("#app element not found");

app.innerHTML = `
  <div id="layout">
    <header id="topbar">
      <button id="menu-btn" aria-label="Menu">&#9776;</button>
      <h1>Mesh</h1>
      <span id="peer-count">peers: 0</span>
    </header>
    <div id="body">
      <aside id="sidebar">
        <ul id="peer-list"></ul>
      </aside>
      <main id="content">
        <p id="welcome">Welcome to Mesh. Open the menu to connect.</p>
      </main>
    </div>

    <div id="video-grid" style="display:none">
      <div id="video-screen" class="video-tile screen" style="display:none"><span class="tile-label">Screen</span></div>
      <div id="video-local" class="video-tile"><span class="tile-label">You</span></div>
      <div id="video-remote" class="video-tile"><span class="tile-label">Peer</span></div>
    </div>

    <footer id="toolbar">
      <button id="btn-mic" class="toolbar-btn" disabled title="Microphone">🎤</button>
      <button id="btn-cam" class="toolbar-btn" disabled title="Camera">📷</button>
      <button id="btn-screen" class="toolbar-btn" disabled title="Share screen">🖥</button>
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
let pendingMediaKind = null;  // tracks what type of media the remote is about to send

document.addEventListener("mesh-connected", (e) => {
  const { pc, chat, file, signal, displayName } = e.detail;
  activePC = pc;
  activeSignalDC = signal;
  localStream = null;
  screenStream = null;

  overlay.style.display = "none";

  const content = document.getElementById("content");
  content.innerHTML = "";
  attachChatUI(content, { dc: chat, dcFile: file, displayName });

  setupRenegotiation(pc, signal, {
    onRemoteTrack: (stream) => {
      if (pendingMediaKind === "screen") {
        pendingMediaKind = null;
        showScreenTile(stream, false);
      } else {
        const tile = document.getElementById("video-remote");
        tile.innerHTML = `<span class="tile-label">Peer</span>`;
        tile.appendChild(createVideoElement(stream, false));
      }
      showGrid();
    },
  });

  // Listen for media metadata from peer
  signal.addEventListener("message", (e) => {
    let msg;
    try { msg = JSON.parse(e.data); } catch { return; }
    if (msg.type === "media-kind") pendingMediaKind = msg.kind;
  });

  document.getElementById("btn-mic").disabled = false;
  document.getElementById("btn-cam").disabled = false;
  document.getElementById("btn-screen").disabled = false;
});

// ── Video grid helpers ──

function showGrid() {
  const grid = document.getElementById("video-grid");
  grid.style.display = "flex";
}

function hideGridIfEmpty() {
  const grid = document.getElementById("video-grid");
  if (!grid.querySelector("video")) {
    grid.style.display = "none";
  }
}

function showScreenTile(stream, local) {
  const tile = document.getElementById("video-screen");
  tile.style.display = "block";
  tile.innerHTML = `<span class="tile-label">Screen</span>`;
  tile.appendChild(createVideoElement(stream, local));
  document.getElementById("video-grid").classList.add("has-screen");
}

function hideScreenTile() {
  const tile = document.getElementById("video-screen");
  tile.style.display = "none";
  tile.innerHTML = `<span class="tile-label">Screen</span>`;
  document.getElementById("video-grid").classList.remove("has-screen");
}

function showLocalVideo(stream) {
  const tile = document.getElementById("video-local");
  tile.innerHTML = `<span class="tile-label">You</span>`;
  tile.appendChild(createVideoElement(stream, true));
  showGrid();
}

function hideLocalVideo() {
  const tile = document.getElementById("video-local");
  tile.innerHTML = `<span class="tile-label">You</span>`;
  if (!document.getElementById("video-remote").querySelector("video")) {
    hideGridIfEmpty();
  }
}

// ── Media controls ──

const btnMic = document.getElementById("btn-mic");
const btnCam = document.getElementById("btn-cam");
const btnScreen = document.getElementById("btn-screen");

btnMic.addEventListener("click", toggleMic);
btnCam.addEventListener("click", toggleCam);
btnScreen.addEventListener("click", toggleScreen);

async function toggleMic() {
  if (!activePC) return;
  if (localStream?.getAudioTracks().length) {
    removeMediaFromPC(activePC, localStream);
    if (!localStream.getVideoTracks().length) {
      stopStream(localStream);
      localStream = null;
    }
    btnMic.classList.remove("active");
    return;
  }
  try {
    if (!localStream) localStream = await getStream({ audio: true, video: true });
    else {
      const s = await getStream({ audio: true, video: false });
      for (const t of s.getAudioTracks()) localStream.addTrack(t);
    }
    addMediaToPC(activePC, localStream);
    showLocalVideo(localStream);
    btnMic.classList.add("active");
  } catch (err) {
    alert(err.message);
  }
}

async function toggleCam() {
  if (!activePC) return;
  if (localStream?.getVideoTracks().length) {
    removeMediaFromPC(activePC, localStream);
    if (!localStream.getAudioTracks().length) {
      stopStream(localStream);
      localStream = null;
    }
    btnCam.classList.remove("active");
    hideLocalVideo();
    return;
  }
  try {
    if (!localStream) localStream = await getStream({ audio: true, video: true });
    else {
      const s = await getStream({ audio: false, video: true });
      for (const t of s.getVideoTracks()) localStream.addTrack(t);
    }
    addMediaToPC(activePC, localStream);
    showLocalVideo(localStream);
    btnCam.classList.add("active");
  } catch (err) {
    alert(err.message);
  }
}

async function toggleScreen() {
  if (!activePC) return;
  if (screenStream) {
    removeMediaFromPC(activePC, screenStream);
    stopStream(screenStream);
    screenStream = null;
    btnScreen.classList.remove("active");
    hideScreenTile();
    hideGridIfEmpty();
    return;
  }
  try {
    const stream = await getScreenStream();
    if (!stream) return;  // user cancelled picker
    screenStream = stream;

    // When the user stops sharing via the browser's native "Stop sharing" button
    stream.getVideoTracks()[0]?.addEventListener("ended", () => {
      removeMediaFromPC(activePC, screenStream);
      stopStream(screenStream);
      screenStream = null;
      btnScreen.classList.remove("active");
      hideScreenTile();
      hideGridIfEmpty();
    });

    addMediaToPC(activePC, screenStream);
    activeSignalDC?.send(JSON.stringify({ type: "media-kind", kind: "screen" }));
    showScreenTile(screenStream, true);
    btnScreen.classList.add("active");
  } catch (err) {
    if (err.name !== "AbortError") alert(err.message);
  }
}
