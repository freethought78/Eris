import { createOffer, createRoomAnswer, acceptRoomAnswer } from "../webrtc/peer.js";
import { attachChatUI } from "./chat.js";

const isLocal = window.location.protocol === "file:";

function wsUrl(host) {
  return `ws://${host}/signal`;
}

async function sha256(str) {
  const hash = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(str));
  return Array.from(new Uint8Array(hash)).map((b) => b.toString(16).padStart(2, "0")).join("");
}

export function attachRoomUI(container) {
  container.innerHTML = `
    <h2>Room Connection</h2>
    <p class="help-text">Requires a Mesh server running on your network.</p>

    <label class="field">
      <span>Display name</span>
      <input type="text" id="room-name-input" placeholder="Alice">
    </label>

    <label class="field">
      <span>Server address</span>
      <input type="text" id="room-server" placeholder="192.168.1.100:8080"
        value="${isLocal ? "" : window.location.host}">
    </label>

    <label class="field">
      <span>Room name</span>
      <input type="text" id="room-room" placeholder="my-room">
    </label>

    <label class="field">
      <span>Password</span>
      <input type="password" id="room-password" placeholder="Room password">
    </label>

    <div id="room-buttons">
      <button id="btn-create-room" class="btn">Create Room</button>
      <button id="btn-join-room" class="btn">Join Room</button>
    </div>

    <div id="room-status" class="status-box" style="display:none"></div>
  `;

  let ws = null;
  let pc = null;
  let dcChat = null;
  let dcFile = null;

  const nameInput = container.querySelector("#room-name-input");
  const displayName = () => nameInput.value.trim() || "You";

  const serverInput = container.querySelector("#room-server");
  const roomInput = container.querySelector("#room-room");
  const passInput = container.querySelector("#room-password");
  const statusDiv = container.querySelector("#room-status");

  function setStatus(text, className) {
    statusDiv.style.display = "block";
    statusDiv.textContent = text;
    statusDiv.className = "status-box" + (className ? " " + className : "");
  }

  function onChannelsOpen({ chat, file }) {
    dcChat = chat;
    dcFile = file;
    setStatus("Connected!", "connected");
    attachChatUI(container, { dc: chat, dcFile: file, displayName: displayName() });
  }

  function sendToServer(obj) {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(obj));
    }
  }

  function connectWs(host, room, passwordHash, mode) {
    const url = wsUrl(host);
    setStatus("Connecting to server...");
    ws = new WebSocket(url);

    ws.onopen = () => {
      sendToServer({ type: mode, room, password: passwordHash });
    };

    ws.onmessage = async (event) => {
      let msg;
      try {
        msg = JSON.parse(event.data);
      } catch {
        return;
      }

      if (msg.type === "error") {
        setStatus("Error: " + msg.message, "error");
        return;
      }

      if (msg.type === "created") {
        setStatus("Room created. Waiting for someone to join...", "waiting");
        return;
      }

      if (msg.type === "joined") {
        setStatus("Joined room. Waiting for offer...", "waiting");
        return;
      }

      if (msg.type === "peer-joined") {
        setStatus("Peer joined! Establishing connection...", "waiting");
        try {
          const result = await createOffer(onChannelsOpen);
          pc = result.pc;
          sendToServer({ type: "signal", room, data: { type: "offer", sdp: result.sdp } });
          setStatus("Offer sent, waiting for answer...", "waiting");
        } catch (err) {
          setStatus("Failed to create offer: " + err.message, "error");
        }
        return;
      }

      if (msg.type === "signal") {
        const { data } = msg;
        if (!data) return;

        if (data.type === "offer") {
          try {
            const result = await createRoomAnswer(data.sdp, onChannelsOpen);
            pc = result.pc;
            sendToServer({ type: "signal", room, data: { type: "answer", sdp: result.sdp } });
            setStatus("Answer sent, establishing connection...", "waiting");
          } catch (err) {
            setStatus("Failed to create answer: " + err.message, "error");
          }
        } else if (data.type === "answer") {
          try {
            await acceptRoomAnswer(pc, data.sdp);
          } catch (err) {
            setStatus("Failed to accept answer: " + err.message, "error");
          }
        }
        return;
      }

      if (msg.type === "peer-left") {
        setStatus("Peer disconnected", "error");
        return;
      }
    };

    ws.onerror = () => {
      setStatus("WebSocket connection failed", "error");
    };

    ws.onclose = () => {
      if (statusDiv.textContent !== "Connected!") {
        setStatus("Disconnected from server", "error");
      }
    };
  }

  function cleanup() {
    if (ws) { ws.onclose = null; ws.close(); ws = null; }
    if (pc) { pc.close(); pc = null; }
    dcChat = null;
    dcFile = null;
  }

  async function handleCreate() {
    const server = serverInput.value.trim();
    const name = roomInput.value.trim();
    const pass = passInput.value;
    if (!server) { setStatus("Enter a server address", "error"); return; }
    if (!name) { setStatus("Enter a room name", "error"); return; }
    cleanup();
    const hash = pass ? await sha256(pass) : "";
    connectWs(server, name, hash, "create");
  }

  async function handleJoin() {
    const server = serverInput.value.trim();
    const name = roomInput.value.trim();
    const pass = passInput.value;
    if (!server) { setStatus("Enter a server address", "error"); return; }
    if (!name) { setStatus("Enter a room name", "error"); return; }
    cleanup();
    const hash = pass ? await sha256(pass) : "";
    connectWs(server, name, hash, "join");
  }

  container.querySelector("#btn-create-room").addEventListener("click", handleCreate);
  container.querySelector("#btn-join-room").addEventListener("click", handleJoin);
}
