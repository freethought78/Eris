import { attachManualUI } from "./manual.js";

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
    <footer id="toolbar">
      <span>Toolbar — coming in later phases</span>
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
          <button class="tab" data-mode="room" disabled>Room (coming soon)</button>
        </div>
        <div id="modal-content"></div>
      </div>
    </div>
  </div>
`;

// Menu toggle
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

// Modal
const overlay = document.getElementById("modal-overlay");
const modalClose = document.getElementById("modal-close");
const modalContent = document.getElementById("modal-content");

document.getElementById("menu-connect").addEventListener("click", () => {
  menuOpen = false;
  menuPanel.classList.remove("open");
  overlay.style.display = "flex";
  attachManualUI(modalContent);
});

modalClose.addEventListener("click", () => {
  overlay.style.display = "none";
});

overlay.addEventListener("click", (e) => {
  if (e.target === overlay) overlay.style.display = "none";
});
