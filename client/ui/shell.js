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
`;
