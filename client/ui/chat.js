import { createTextChannel } from "../channels/text.js";

export function attachChatUI(container, { dc, displayName }) {
  const chat = document.createElement("div");
  chat.id = "chat-ui";
  chat.innerHTML = `
    <div id="chat-messages"></div>
    <div id="chat-input-bar">
      <input type="text" id="chat-input" placeholder="Type a message..." autocomplete="off">
      <button id="chat-send" class="btn small">Send</button>
    </div>
  `;
  container.appendChild(chat);

  const msgContainer = chat.querySelector("#chat-messages");
  const input = chat.querySelector("#chat-input");
  const sendBtn = chat.querySelector("#chat-send");

  const channel = createTextChannel(dc, {
    onMessage: (msg) => addMessage(msg.from, msg.text, msg.ts),
  });

  function addMessage(from, text, ts) {
    const div = document.createElement("div");
    div.className = "chat-msg";
    const time = ts ? new Date(ts).toLocaleTimeString() : "";
    div.innerHTML = `<span class="chat-sender">${escapeHtml(from)}</span> <span class="chat-time">${time}</span><br><span class="chat-text">${escapeHtml(text)}</span>`;
    msgContainer.appendChild(div);
    msgContainer.scrollTop = msgContainer.scrollHeight;
  }

  function addFileMessage(from, text, progressEl) {
    const div = document.createElement("div");
    div.className = "chat-msg chat-file-msg";
    const time = new Date().toLocaleTimeString();
    div.innerHTML = `<span class="chat-sender">${escapeHtml(from)}</span> <span class="chat-time">${time}</span><br><span class="chat-text">${escapeHtml(text)}</span>`;
    if (progressEl) div.querySelector(".chat-text")?.after(progressEl);
    msgContainer.appendChild(div);
    msgContainer.scrollTop = msgContainer.scrollHeight;
    return div;
  }

  function send() {
    const text = input.value.trim();
    if (!text) return;
    channel.send(text, displayName);
    addMessage(displayName || "You", text, Date.now());
    input.value = "";
    input.focus();
  }

  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter") send();
  });
  sendBtn.addEventListener("click", send);

  setTimeout(() => input.focus(), 100);

  // Expose file transfer display helpers
  const progressBars = new Map();

  return {
    showFileProgress(name, chunk, total, direction) {
      const key = escapeId(name);
      let bar = document.getElementById("file-progress-" + key);
      if (!bar) {
        const label = direction === "send" ? "Sending" : "Receiving";
        bar = document.createElement("progress");
        bar.id = "file-progress-" + key;
        bar.className = "file-progress";
        bar.max = total;
        bar.value = 0;
        addFileMessage(direction === "send" ? "You" : "Peer", label + ": " + escapeHtml(name), bar);
      }
      bar.max = total;
      bar.value = chunk;
    },

    showFileComplete(name, direction) {
      addMessage(direction === "send" ? "You" : "Peer", (direction === "send" ? "Sent" : "Received") + ": " + name, Date.now());
    },

    showFileError(name, err) {
      addMessage("System", "Failed to transfer " + name + ": " + (err.message || err), Date.now());
    },
  };
}

function escapeHtml(s) {
  const d = document.createElement("div");
  d.textContent = s;
  return d.innerHTML;
}

function escapeId(s) {
  return s.replace(/[^a-zA-Z0-9_-]/g, "_");
}
