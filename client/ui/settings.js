let overlay = null;

export async function openSettingsPanel(opts) {
  closeSettingsPanel();
  overlay = document.createElement("div");
  overlay.className = "call-overlay";
  overlay.style.zIndex = "400";
  overlay.innerHTML = `
    <div class="settings-box">
      <div class="settings-header">
        <h2>Settings</h2>
        <button class="settings-close" aria-label="Close">&times;</button>
      </div>
      <div class="settings-body" id="settings-body"></div>
    </div>
  `;
  overlay.querySelector(".settings-close").addEventListener("click", closeSettingsPanel);
  overlay.addEventListener("click", (e) => { if (e.target === overlay) closeSettingsPanel(); });
  document.body.appendChild(overlay);

  const body = overlay.querySelector("#settings-body");
  body.innerHTML = "<p>Loading devices...</p>";
  await renderDevices(body, opts);
}

function closeSettingsPanel() {
  if (overlay) { overlay.remove(); overlay = null; }
}

async function renderDevices(container, opts) {
  let devices;
  try {
    devices = await navigator.mediaDevices.enumerateDevices();
  } catch {
    container.innerHTML = "<p>Cannot access device list.</p>";
    return;
  }

  const groups = { audioinput: [], videoinput: [], audiooutput: [] };
  for (const d of devices) {
    if (groups[d.kind]) groups[d.kind].push(d);
  }

  container.innerHTML = "";

  const entries = [
    { kind: "audioinput", label: "Microphone" },
    { kind: "videoinput", label: "Camera" },
    { kind: "audiooutput", label: "Speaker" },
  ];

  for (const { kind, label } of entries) {
    const devs = groups[kind];
    if (!devs.length) continue;

    const div = document.createElement("div");
    div.className = "field";
    div.innerHTML = `
      <span>${label}</span>
      <select data-kind="${kind}">
        ${devs.map((d) => `<option value="${d.deviceId}">${esc(d.label) || "Default"}</option>`).join("")}
      </select>
    `;
    container.appendChild(div);

    const select = div.querySelector("select");
    select.addEventListener("change", () => changeDevice(kind, select.value, opts));
  }
}

async function changeDevice(kind, deviceId, opts) {
  const { getPC, getLocalStream, setLocalStream } = opts;

  if (kind === "audiooutput") {
    document.querySelectorAll("video").forEach((v) => {
      if (v.setSinkId) v.setSinkId(deviceId).catch(() => {});
    });
    return;
  }

  const mediaKind = kind === "audioinput" ? "audio" : "video";
  const trackKind = kind === "audioinput" ? "audio" : "video";

  try {
    const stream = await navigator.mediaDevices.getUserMedia({
      [mediaKind]: { deviceId: { exact: deviceId } },
    });
    const newTrack = stream.getTracks()[0];
    if (!newTrack) return;

    const pc = getPC();
    const localStream = getLocalStream();

    // Replace track in peer connection
    const sender = pc?.getSenders().find((s) => s.track?.kind === trackKind);
    if (sender && newTrack) await sender.replaceTrack(newTrack);

    // Update local stream
    if (localStream) {
      const oldTrack = localStream.getTracks().find((t) => t.kind === trackKind);
      if (oldTrack) {
        localStream.removeTrack(oldTrack);
        oldTrack.stop();
      }
      localStream.addTrack(newTrack);
    } else {
      setLocalStream(stream);
    }

    // Refresh local video tile if camera changed
    if (kind === "videoinput") {
      const tile = document.querySelector('[data-tile-id="local-cam"] video');
      if (tile) tile.srcObject = localStream || stream;
    }
  } catch (err) {
    alert("Failed to switch " + (kind === "audioinput" ? "microphone" : "camera") + ": " + err.message);
  }
}

function esc(s) {
  const d = document.createElement("div");
  d.textContent = s;
  return d.innerHTML;
}
