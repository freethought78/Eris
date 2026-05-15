const CHUNK_SIZE = 65536;
let signalDC = null;
let fileDC = null;
let outgoing = null; // { id, file, aborted }
let incoming = null; // { id, name, size, chunks, total, writable, aborted }
let callbacks = {};

// Overlay refs
let pendingOverlay = null;
let incomingOverlay = null;

export function initFileTransfer(sig, file, cb) {
  signalDC = sig;
  fileDC = file;
  callbacks = cb || {};

  sig.addEventListener("message", (e) => {
    let msg;
    try { msg = JSON.parse(e.data); } catch { return; }
    switch (msg.type) {
      case "file-request": onFileRequest(msg); break;
      case "file-accept": onFileAccept(msg); break;
      case "file-decline": onFileDecline(msg); break;
      case "file-cancel": onFileCancel(msg); break;
    }
  });

  // Listen for incoming chunks
  fileDC.addEventListener("message", (e) => {
    let msg;
    try { msg = JSON.parse(e.data); } catch { return; }
    if (typeof msg.id !== "string" || typeof msg.data !== "string") return;
    onChunk(msg);
  });
}

// ── Sender side ──

export function startFileSend(file) {
  if (outgoing) return;
  showSendConfirm(file);
}

function showSendConfirm(file) {
  removeOverlays();
  pendingOverlay = overlay(`
    <div class="ft-box">
      <p class="ft-label">Send File</p>
      <p class="ft-name">${esc(file.name)}</p>
      <p class="ft-size">${formatSize(file.size)}</p>
      <div class="call-overlay-buttons">
        <button class="call-btn call-btn-decline" id="ft-send-cancel">Cancel</button>
        <button class="call-btn call-btn-accept" id="ft-send-confirm">Send</button>
      </div>
    </div>
  `);
  pendingOverlay.querySelector("#ft-send-cancel").onclick = () => { removeOverlays(); outgoing = null; };
  pendingOverlay.querySelector("#ft-send-confirm").onclick = async () => {
    const id = crypto.randomUUID();
    outgoing = { id, file, aborted: false };
    removeOverlays();
    signalDC?.send(JSON.stringify({ type: "file-request", id, name: file.name, size: file.size }));
    showPendingSend(id, file.name);
  };
}

function showPendingSend(id, name) {
  removeOverlays();
  pendingOverlay = overlay(`
    <div class="ft-box">
      <p class="ft-label">Sending file...</p>
      <p class="ft-name">${esc(name)}</p>
      <p class="ft-size">Waiting for peer to accept...</p>
      <div class="call-overlay-buttons">
        <button class="call-btn call-btn-decline" id="ft-pending-cancel">Cancel</button>
      </div>
    </div>
  `);
  pendingOverlay.querySelector("#ft-pending-cancel").onclick = () => {
    cancelTransfer();
  };
}

async function onFileAccept(msg) {
  if (!outgoing || outgoing.id !== msg.id || outgoing.aborted) return;
  removeOverlays();
  callbacks.onProgress?.({ name: outgoing.file.name, chunk: 0, total: Math.ceil(outgoing.file.size / CHUNK_SIZE), direction: "send" });
  await doSend(outgoing.id, outgoing.file);
}

function onFileDecline(msg) {
  if (!outgoing || outgoing.id !== msg.id) return;
  removeOverlays();
  showToast("File transfer declined: " + outgoing.file.name);
  outgoing = null;
}

async function doSend(id, file) {
  const total = Math.ceil(file.size / CHUNK_SIZE);
  let chunk = 0;

  return new Promise((resolve) => {
    const reader = new FileReader();
    reader.onerror = () => { callbacks.onError?.({ name: file.name, error: reader.error }); resolve(); };
    reader.onload = () => {
      if (outgoing?.aborted) { resolve(); return; }
      const data = bufToBase64(reader.result);
      fileDC?.send(JSON.stringify({ id, name: file.name, size: file.size, chunk, total, data }));
      chunk++;
      callbacks.onProgress?.({ name: file.name, chunk, total, direction: "send" });
      if (chunk < total) readNext();
      else {
        callbacks.onComplete?.({ name: file.name, direction: "send" });
        outgoing = null;
        resolve();
      }
    };
    function readNext() {
      const start = chunk * CHUNK_SIZE;
      const end = Math.min(start + CHUNK_SIZE, file.size);
      reader.readAsArrayBuffer(file.slice(start, end));
    }
    readNext();
  });
}

// ── Receiver side ──

function onFileRequest(msg) {
  if (incoming) {
    signalDC?.send(JSON.stringify({ type: "file-decline", id: msg.id }));
    return;
  }
  incoming = { id: msg.id, name: msg.name, size: msg.size, chunks: [], total: msg.total || Math.ceil(msg.size / CHUNK_SIZE), writable: null, aborted: false };
  showIncomingFile(msg);
}

function showIncomingFile(msg) {
  removeOverlays();
  incomingOverlay = overlay(`
    <div class="ft-box">
      <p class="ft-label">Incoming File</p>
      <p class="ft-name">${esc(msg.name)}</p>
      <p class="ft-size">${formatSize(msg.size)}</p>
      <div class="call-overlay-buttons">
        <button class="call-btn call-btn-decline" id="ft-incoming-decline">Decline</button>
        <button class="call-btn call-btn-accept" id="ft-incoming-accept">Accept</button>
      </div>
    </div>
  `);
  incomingOverlay.querySelector("#ft-incoming-decline").onclick = () => {
    signalDC?.send(JSON.stringify({ type: "file-decline", id: msg.id }));
    removeOverlays();
    incoming = null;
  };
  incomingOverlay.querySelector("#ft-incoming-accept").onclick = async () => {
    let writable = null;
    if (window.showSaveFilePicker) {
      try {
        const handle = await window.showSaveFilePicker({ suggestedName: msg.name });
        writable = await handle.createWritable();
      } catch { return; } // user cancelled save dialog
    } else {
      // FS API not available — ask user to confirm download
      const ok = await showDownloadConfirm(msg.name);
      if (!ok) {
        signalDC?.send(JSON.stringify({ type: "file-decline", id: msg.id }));
        incoming = null;
        return;
      }
    }
    incoming.writable = writable;
    removeOverlays();
    signalDC?.send(JSON.stringify({ type: "file-accept", id: msg.id }));
    callbacks.onProgress?.({ name: msg.name, chunk: 0, total: incoming.total, direction: "receive" });
  };
}

function onChunk(msg) {
  if (!incoming || incoming.id !== msg.id || incoming.aborted) return;
  if (!incoming.total) incoming.total = msg.total;

  if (msg.chunk === 0 && msg.name) incoming.name = msg.name;
  incoming.chunks[msg.chunk] = msg.data;
  callbacks.onProgress?.({ name: incoming.name, chunk: incoming.chunks.length, total: incoming.total, direction: "receive" });

  if (incoming.chunks.length === incoming.total && incoming.chunks.every((c) => c !== undefined)) {
    finishReceive();
  }
}

async function finishReceive() {
  const parts = incoming.chunks.map((c) => base64ToBuf(c));
  const totalLen = parts.reduce((a, b) => a + b.byteLength, 0);
  const combined = new Uint8Array(totalLen);
  let offset = 0;
  for (const part of parts) {
    combined.set(new Uint8Array(part), offset);
    offset += part.byteLength;
  }
  const blob = new Blob([combined]);

  if (incoming.writable) {
    try {
      await incoming.writable.write(blob);
      await incoming.writable.close();
      showToast("File saved: " + incoming.name);
    } catch (err) {
      callbacks.onError?.({ name: incoming.name, error: err });
    }
  } else {
    // Fallback: download via link
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = incoming.name;
    a.click();
    URL.revokeObjectURL(url);
  }

  callbacks.onComplete?.({ name: incoming.name, direction: "receive" });
  incoming = null;
}

// ── Cancel ──

export function cancelTransfer() {
  if (outgoing && !outgoing.aborted) {
    outgoing.aborted = true;
    signalDC?.send(JSON.stringify({ type: "file-cancel", id: outgoing.id }));
    showToast("Transfer cancelled");
    outgoing = null;
  }
  if (incoming && !incoming.aborted) {
    incoming.aborted = true;
    if (incoming.writable) incoming.writable.close().catch(() => {});
    signalDC?.send(JSON.stringify({ type: "file-cancel", id: incoming.id }));
    showToast("Transfer cancelled");
    incoming = null;
  }
  removeOverlays();
}

function onFileCancel(msg) {
  if (outgoing && outgoing.id === msg.id) {
    outgoing.aborted = true;
    showToast("Peer cancelled the transfer");
    outgoing = null;
    removeOverlays();
  }
  if (incoming && incoming.id === msg.id) {
    incoming.aborted = true;
    if (incoming.writable) incoming.writable.close().catch(() => {});
    showToast("Peer cancelled the transfer");
    incoming = null;
    removeOverlays();
  }
}

// ── Download confirm (fallback when showSaveFilePicker unavailable) ──

function showDownloadConfirm(name) {
  return new Promise((resolve) => {
    const el = overlay(`
      <div class="ft-box">
        <p class="ft-label">Save File</p>
        <p class="ft-name">${esc(name)}</p>
        <p class="ft-size">File will be saved to your Downloads folder.</p>
        <div class="call-overlay-buttons">
          <button class="call-btn call-btn-decline" id="ft-dl-cancel">Cancel</button>
          <button class="call-btn call-btn-accept" id="ft-dl-start">Start Download</button>
        </div>
      </div>
    `);
    el.querySelector("#ft-dl-cancel").onclick = () => { el.remove(); resolve(false); };
    el.querySelector("#ft-dl-start").onclick = () => { el.remove(); resolve(true); };
  });
}

// ── Helpers ──

function overlay(html) {
  const el = document.createElement("div");
  el.className = "call-overlay";
  el.style.zIndex = "350";
  el.innerHTML = `<div style="display:flex;align-items:center;justify-content:center;min-height:100%">${html}</div>`;
  document.body.appendChild(el);
  return el;
}

function removeOverlays() {
  if (pendingOverlay) { pendingOverlay.remove(); pendingOverlay = null; }
  if (incomingOverlay) { incomingOverlay.remove(); incomingOverlay = null; }
}

function showToast(text) {
  const toast = document.createElement("div");
  toast.className = "call-toast";
  toast.textContent = text;
  document.body.appendChild(toast);
  setTimeout(() => toast.remove(), 3000);
}

function bufToBase64(buf) {
  const bytes = new Uint8Array(buf);
  let s = "";
  for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
  return btoa(s);
}

function base64ToBuf(s) {
  const s2 = atob(s);
  const bytes = new Uint8Array(s2.length);
  for (let i = 0; i < s2.length; i++) bytes[i] = s2.charCodeAt(i);
  return bytes.buffer;
}

function formatSize(bytes) {
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1048576) return (bytes / 1024).toFixed(1) + " KB";
  return (bytes / 1048576).toFixed(1) + " MB";
}

function esc(s) {
  const d = document.createElement("div");
  d.textContent = s;
  return d.innerHTML;
}
