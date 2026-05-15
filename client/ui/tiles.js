let grid = null;
const tileMap = new Map();
let maximizedId = null;
let draggedId = null;

export function initTileGrid(gridEl) {
  grid = gridEl;
  grid.addEventListener("dragover", onDragOver);
  grid.addEventListener("drop", onDrop);
  grid.addEventListener("dragend", () => { draggedId = null; });
}

export function addTile(id, label, stream, isLocal) {
  removeTile(id);

  const tile = document.createElement("div");
  tile.className = "video-tile";
  tile.dataset.tileId = id;
  tile.draggable = true;

  const isScreen = label.toLowerCase() === "screen";

  const controls = document.createElement("div");
  controls.className = "tile-controls";
  controls.innerHTML = `
    <span class="tile-label">${esc(label)}</span>
    <div class="tile-buttons">
      <button class="tile-btn" data-action="maximize" title="Maximize">⛶</button>
      <button class="tile-btn" data-action="fullscreen" title="Fullscreen">⛶</button>
      <button class="tile-btn" data-action="popout" title="Pop out">🖼</button>
      <button class="tile-btn" data-action="hide" title="Hide">✕</button>
    </div>
  `;
  tile.appendChild(controls);

  const video = document.createElement("video");
  video.srcObject = stream;
  video.autoplay = true;
  video.playsInline = true;
  video.muted = isLocal;
  if (isScreen) video.dataset.screen = "";
  tile.appendChild(video);

  tile.addEventListener("click", () => toggleMaximize(id));
  controls.querySelectorAll(".tile-btn").forEach((btn) => {
    const action = btn.dataset.action;
    const handler = btnHandlers[action];
    if (handler) btn.addEventListener("click", (e) => { e.stopPropagation(); handler(id, tile); });
  });

  tile.addEventListener("dragstart", (e) => {
    draggedId = id;
    e.dataTransfer.effectAllowed = "move";
  });

  tileMap.set(id, tile);
  grid.appendChild(tile);
  return tile;
}

export function removeTile(id) {
  const tile = tileMap.get(id);
  if (!tile) return;
  tile.remove();
  tileMap.delete(id);
  if (maximizedId === id) maximizedId = null;
}

export function hideTile(id) {
  const tile = tileMap.get(id);
  if (tile) tile.style.display = "none";
}

export function showTile(id) {
  const tile = tileMap.get(id);
  if (tile) tile.style.display = "";
}

export function toggleMaximize(id) {
  if (maximizedId === id) {
    maximizedId = null;
    tileMap.forEach((t) => {
      t.style.display = "";
      t.classList.remove("maximized");
    });
  } else {
    maximizedId = id;
    tileMap.forEach((t, tid) => {
      if (tid === id) {
        t.style.display = "";
        t.classList.add("maximized");
      } else {
        t.style.display = "none";
      }
    });
  }
}

export function hasTile(id) {
  return tileMap.has(id);
}

// ── Drag & drop ──

function onDragOver(e) {
  e.preventDefault();
  e.dataTransfer.dropEffect = "move";
}

function onDrop(e) {
  e.preventDefault();
  if (!draggedId || !grid) return;
  const target = e.target.closest(".video-tile");
  if (!target) return;
  const targetId = target.dataset.tileId;
  if (draggedId === targetId) return;

  const draggedTile = tileMap.get(draggedId);
  const targetTile = tileMap.get(targetId);
  if (!draggedTile || !targetTile) return;

  const rect = targetTile.getBoundingClientRect();
  const midY = rect.top + rect.height / 2;
  if (e.clientY < midY) {
    grid.insertBefore(draggedTile, targetTile);
  } else {
    grid.insertBefore(draggedTile, targetTile.nextSibling);
  }
  draggedId = null;
}

// ── Button handlers ──

const btnHandlers = {
  maximize(id) {
    toggleMaximize(id);
    const btn = tileMap.get(id)?.querySelector('[data-action="maximize"]');
    if (btn) btn.textContent = maximizedId === id ? "⤡" : "⛶";
  },

  fullscreen(_id, tile) {
    const video = tile.querySelector("video");
    if (video) video.requestFullscreen();
  },

  popout(_id, tile) {
    const video = tile.querySelector("video");
    if (video && document.pictureInPictureEnabled) {
      if (document.pictureInPictureElement === video) {
        document.exitPictureInPicture();
      } else {
        video.requestPictureInPicture();
      }
    }
  },

  hide(id) {
    hideTile(id);
  },
};

function esc(s) {
  const d = document.createElement("div");
  d.textContent = s;
  return d.innerHTML;
}
