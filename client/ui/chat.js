import { createTextChannel } from "../channels/text.js";
import { sendFile, receiveFile } from "../channels/file.js";

export function attachChatUI(container, { dc, dcFile, displayName }) {
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
  const filePicker = document.getElementById("chat-file-picker");

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

  // File picker
  filePicker.addEventListener("change", async () => {
    const file = filePicker.files?.[0];
    if (!file || !dcFile) return;
    filePicker.value = "";

    const bar = document.createElement("progress");
    bar.className = "file-progress";
    bar.max = 1;
    bar.value = 0;

    const msgDiv = addFileMessage(displayName || "You", `Sending: ${escapeHtml(file.name)}`, bar);
    addMessage("You", `Sending file: ${file.name} (${formatSize(file.size)})`, Date.now());

    try {
      await sendFile(dcFile, file, {
        onProgress: ({ chunk, total }) => {
          bar.max = total;
          bar.value = chunk;
        },
      });
      addMessage("You", `Sent: ${file.name}`, Date.now());
    } catch (err) {
      addMessage("System", `Failed to send ${file.name}: ${err.message}`, Date.now());
    }
  });

  // File receive
  if (dcFile) {
    receiveFile(dcFile, {
      onProgress: ({ name, chunk, total }) => {
        let bar = document.getElementById("file-progress-" + escapeId(name));
        if (!bar) {
          bar = document.createElement("progress");
          bar.id = "file-progress-" + escapeId(name);
          bar.className = "file-progress";
          bar.max = total;
          bar.value = 0;
          addFileMessage("Peer", `Receiving: ${escapeHtml(name)}`, bar);
        }
        bar.max = total;
        bar.value = chunk;
      },
      onComplete: ({ name, blob }) => {
        addMessage("Peer", `Received: ${name}`, Date.now());
        const url = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = url;
        a.download = name;
        a.textContent = `⬇ Download ${name}`;
        a.className = "file-download";
        const div = document.createElement("div");
        div.className = "chat-msg";
        div.innerHTML = `<span class="chat-sender">Peer</span> <span class="chat-time">${new Date().toLocaleTimeString()}</span><br>`;
        div.appendChild(a);
        msgContainer.appendChild(div);
        msgContainer.scrollTop = msgContainer.scrollHeight;
      },
    });
  }

  setTimeout(() => input.focus(), 100);
}

function formatSize(bytes) {
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1048576) return (bytes / 1024).toFixed(1) + " KB";
  return (bytes / 1048576).toFixed(1) + " MB";
}

function escapeHtml(s) {
  const d = document.createElement("div");
  d.textContent = s;
  return d.innerHTML;
}

function escapeId(s) {
  return s.replace(/[^a-zA-Z0-9_-]/g, "_");
}
