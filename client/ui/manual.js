import { createOffer, createAnswer, acceptAnswer, encryptSDP, decryptSDP } from "../webrtc/peer.js";

export function attachManualUI(container) {
  container.innerHTML = `
    <h2>Manual Connection</h2>
    <p class="help-text">No server needed. Share a code with your contact.</p>

    <label class="field">
      <span>Passphrase (optional — used for SDP encryption)</span>
      <input type="password" id="manual-passphrase" placeholder="Shared passphrase">
    </label>

    <fieldset id="offer-section">
      <legend>1. Generate your offer</legend>
      <button id="btn-generate-offer" class="btn">Generate Offer</button>
      <div id="offer-output" class="sdp-box" style="display:none">
        <textarea id="offer-text" readonly rows="4"></textarea>
        <button id="btn-copy-offer" class="btn small">Copy</button>
      </div>
    </fieldset>

    <fieldset id="answer-section">
      <legend>2. Paste offer from peer</legend>
      <textarea id="input-offer" placeholder="Paste the offer you received here..." rows="4"></textarea>
      <button id="btn-generate-answer" class="btn" disabled>Generate Answer</button>
      <div id="answer-output" class="sdp-box" style="display:none">
        <textarea id="answer-text" readonly rows="4"></textarea>
        <button id="btn-copy-answer" class="btn small">Copy</button>
      </div>
    </fieldset>

    <fieldset id="connect-section">
      <legend>3. Paste answer from peer</legend>
      <textarea id="input-answer" placeholder="Paste the answer you received here..." rows="4"></textarea>
      <button id="btn-connect" class="btn" disabled>Connect</button>
    </fieldset>

    <div id="connection-status" style="display:none"></div>
    <div id="chat-log" style="display:none">
      <h3>Messages</h3>
      <div id="messages"></div>
    </div>
  `;

  let pc = null;
  let dc = null;
  let pendingAnswerPc = null;

  const passphraseInput = container.querySelector("#manual-passphrase");
  const passphrase = () => passphraseInput.value || "";

  // Step 1: Generate Offer
  const btnOffer = container.querySelector("#btn-generate-offer");
  const offerOutput = container.querySelector("#offer-output");
  const offerText = container.querySelector("#offer-text");
  const copyOffer = container.querySelector("#btn-copy-offer");

  btnOffer.addEventListener("click", async () => {
    btnOffer.disabled = true;
    btnOffer.textContent = "Generating...";
    try {
      const result = await createOffer(onDataChannel);
      pc = result.pc;
      dc = result.dc;
      const encrypted = await encryptSDP(result.sdp, passphrase());
      offerText.value = encrypted;
      offerOutput.style.display = "block";
    } catch (err) {
      alert("Failed to create offer: " + err.message);
    }
    btnOffer.disabled = false;
    btnOffer.textContent = "Generate Offer";
  });

  copyOffer.addEventListener("click", () => {
    offerText.select();
    navigator.clipboard.writeText(offerText.value).catch(() => {});
  });

  // Step 2: Generate Answer
  const inputOffer = container.querySelector("#input-offer");
  const btnAnswer = container.querySelector("#btn-generate-answer");
  const answerOutput = container.querySelector("#answer-output");
  const answerText = container.querySelector("#answer-text");
  const copyAnswer = container.querySelector("#btn-copy-answer");

  inputOffer.addEventListener("input", () => {
    btnAnswer.disabled = !inputOffer.value.trim();
  });

  btnAnswer.addEventListener("click", async () => {
    btnAnswer.disabled = true;
    btnAnswer.textContent = "Generating...";
    try {
      const result = await createAnswer(inputOffer.value.trim(), passphrase(), onDataChannel);
      pendingAnswerPc = result.pc;
      dc = result.dc;
      const encrypted = await encryptSDP(result.sdp, passphrase());
      answerText.value = encrypted;
      answerOutput.style.display = "block";
    } catch (err) {
      alert("Failed to generate answer: " + err.message);
    }
    btnAnswer.disabled = false;
    btnAnswer.textContent = "Generate Answer";
  });

  copyAnswer.addEventListener("click", () => {
    answerText.select();
    navigator.clipboard.writeText(answerText.value).catch(() => {});
  });

  // Step 3: Connect (set answer)
  const inputAnswer = container.querySelector("#input-answer");
  const btnConnect = container.querySelector("#btn-connect");
  const statusDiv = container.querySelector("#connection-status");

  inputAnswer.addEventListener("input", () => {
    btnConnect.disabled = !inputAnswer.value.trim();
  });

  btnConnect.addEventListener("click", async () => {
    if (!pc) {
      alert("Generate an offer first.");
      return;
    }
    btnConnect.disabled = true;
    btnConnect.textContent = "Connecting...";
    try {
      await acceptAnswer(pc, inputAnswer.value.trim(), passphrase());
    } catch (err) {
      alert("Failed to connect: " + err.message);
      btnConnect.disabled = false;
      btnConnect.textContent = "Connect";
    }
  });

  function onDataChannel(channel) {
    dc = channel;
    statusDiv.style.display = "block";
    statusDiv.textContent = "Connected!";
    statusDiv.className = "connected";

    dc.addEventListener("message", (e) => {
      addMessage("Peer", e.data);
    });

    dc.send("hello — DataChannel is alive");
  }

  function addMessage(from, text) {
    const log = container.querySelector("#chat-log");
    const msgs = container.querySelector("#messages");
    log.style.display = "block";
    const div = document.createElement("div");
    div.className = "msg";
    div.innerHTML = `<strong>${from}:</strong> ${escapeHtml(text)}`;
    msgs.appendChild(div);
  }
}

function escapeHtml(s) {
  const d = document.createElement("div");
  d.textContent = s;
  return d.innerHTML;
}
