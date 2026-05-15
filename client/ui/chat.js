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

  // Focus input after attach
  setTimeout(() => input.focus(), 100);
}

function escapeHtml(s) {
  const d = document.createElement("div");
  d.textContent = s;
  return d.innerHTML;
}
