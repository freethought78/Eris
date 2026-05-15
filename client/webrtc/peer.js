import { encryptSDP, decryptSDP } from "./crypto.js";

const ICE_SERVERS = [
  { urls: "stun:stun.l.google.com:19302" },
  { urls: "stun:stun1.l.google.com:19302" },
  { urls: "stun:stun.cloudflare.com:3478" },
];

export function createPeerConnection() {
  return new RTCPeerConnection({ iceServers: ICE_SERVERS });
}

function waitForIceGathering(pc) {
  return new Promise((resolve) => {
    if (pc.iceGatheringState === "complete") {
      resolve(pc.localDescription.sdp);
      return;
    }
    pc.addEventListener("icegatheringstatechange", () => {
      if (pc.iceGatheringState === "complete") {
        resolve(pc.localDescription.sdp);
      }
    });
  });
}

function whenAllOpen(dcs, cb) {
  const names = Object.keys(dcs);
  const state = {};
  names.forEach((n) => { state[n] = !dcs[n] || dcs[n].readyState === "open"; });
  const check = () => { if (names.every((n) => state[n])) cb(dcs); };
  names.forEach((n) => {
    if (dcs[n] && dcs[n].readyState !== "open") {
      dcs[n].addEventListener("open", () => { state[n] = true; check(); });
    }
  });
  check();
}

export async function createOffer(onChannelsOpen) {
  const pc = createPeerConnection();
  const dcChat = pc.createDataChannel("mesh.chat", { ordered: true });
  const dcFile = pc.createDataChannel("mesh.file", { ordered: true });
  const dcSignal = pc.createDataChannel("mesh.signal", { ordered: true });
  whenAllOpen({ chat: dcChat, file: dcFile, signal: dcSignal }, onChannelsOpen);
  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);
  const sdp = await waitForIceGathering(pc);
  return { pc, dcChat, dcFile, dcSignal, sdp };
}

export async function createAnswer(offerEncoded, passphrase, onChannelsOpen) {
  const offerSdp = await decryptSDP(offerEncoded, passphrase);
  const pc = createPeerConnection();
  const dcs = {};
  const dcLabelMap = { "mesh.chat": "chat", "mesh.file": "file", "mesh.signal": "signal" };
  pc.addEventListener("datachannel", (e) => {
    const key = dcLabelMap[e.channel.label];
    if (key) dcs[key] = e.channel;
    if (dcs.chat && dcs.file && dcs.signal) whenAllOpen(dcs, onChannelsOpen);
  });
  await pc.setRemoteDescription({ type: "offer", sdp: offerSdp });
  const answer = await pc.createAnswer();
  await pc.setLocalDescription(answer);
  const sdp = await waitForIceGathering(pc);
  return { pc, ...dcs, sdp };
}

export async function acceptAnswer(pc, answerEncoded, passphrase) {
  const answerSdp = await decryptSDP(answerEncoded, passphrase);
  await pc.setRemoteDescription({ type: "answer", sdp: answerSdp });
}

export async function createRoomAnswer(offerSdp, onChannelsOpen) {
  const pc = createPeerConnection();
  const dcs = {};
  const dcLabelMap = { "mesh.chat": "chat", "mesh.file": "file", "mesh.signal": "signal" };
  pc.addEventListener("datachannel", (e) => {
    const key = dcLabelMap[e.channel.label];
    if (key) dcs[key] = e.channel;
    if (dcs.chat && dcs.file && dcs.signal) whenAllOpen(dcs, onChannelsOpen);
  });
  await pc.setRemoteDescription({ type: "offer", sdp: offerSdp });
  const answer = await pc.createAnswer();
  await pc.setLocalDescription(answer);
  const sdp = await waitForIceGathering(pc);
  return { pc, ...dcs, sdp };
}

export async function acceptRoomAnswer(pc, answerSdp) {
  await pc.setRemoteDescription({ type: "answer", sdp: answerSdp });
}

export function setupRenegotiation(pc, signalDC, { onRemoteTrack } = {}) {
  let makingOffer = false;

  pc.addEventListener("negotiationneeded", async () => {
    try {
      makingOffer = true;
      await pc.setLocalDescription(await pc.createOffer());
      signalDC.send(JSON.stringify({ type: "reneg-offer", sdp: pc.localDescription.sdp }));
    } catch (err) {
      if (err.name !== "InvalidStateError") throw err;
    } finally {
      makingOffer = false;
    }
  });

  pc.addEventListener("track", (e) => {
    onRemoteTrack?.(e.streams[0], e.track.kind);
  });

  signalDC.addEventListener("message", async (e) => {
    let msg;
    try { msg = JSON.parse(e.data); } catch { return; }

    if (msg.type === "reneg-offer") {
      if (makingOffer && pc.signalingState !== "stable") return;
      await pc.setRemoteDescription({ type: "offer", sdp: msg.sdp });
      await pc.setLocalDescription(await pc.createAnswer());
      signalDC.send(JSON.stringify({ type: "reneg-answer", sdp: pc.localDescription.sdp }));
    } else if (msg.type === "reneg-answer") {
      if (pc.signalingState !== "have-local-offer") return;
      await pc.setRemoteDescription({ type: "answer", sdp: msg.sdp });
    }
  });
}

export { encryptSDP, decryptSDP };
