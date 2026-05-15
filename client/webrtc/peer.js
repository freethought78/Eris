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

function whenBothOpen(dcChat, dcFile, cb) {
  let c = !dcChat || dcChat.readyState === "open";
  let f = !dcFile || dcFile.readyState === "open";
  const check = () => { if (c && f) cb({ chat: dcChat, file: dcFile }); };
  if (dcChat && dcChat.readyState !== "open") dcChat.addEventListener("open", () => { c = true; check(); });
  if (dcFile && dcFile.readyState !== "open") dcFile.addEventListener("open", () => { f = true; check(); });
  check();
}

export async function createOffer(onChannelsOpen) {
  const pc = createPeerConnection();
  const dcChat = pc.createDataChannel("mesh.chat", { ordered: true });
  const dcFile = pc.createDataChannel("mesh.file", { ordered: true });
  whenBothOpen(dcChat, dcFile, onChannelsOpen);
  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);
  const sdp = await waitForIceGathering(pc);
  return { pc, dcChat, dcFile, sdp };
}

export async function createAnswer(offerEncoded, passphrase, onChannelsOpen) {
  const offerSdp = await decryptSDP(offerEncoded, passphrase);
  const pc = createPeerConnection();
  let dcChat = null, dcFile = null;
  pc.addEventListener("datachannel", (e) => {
    if (e.channel.label === "mesh.chat") {
      dcChat = e.channel;
      whenBothOpen(dcChat, dcFile, onChannelsOpen);
    } else if (e.channel.label === "mesh.file") {
      dcFile = e.channel;
      whenBothOpen(dcChat, dcFile, onChannelsOpen);
    }
  });
  await pc.setRemoteDescription({ type: "offer", sdp: offerSdp });
  const answer = await pc.createAnswer();
  await pc.setLocalDescription(answer);
  const sdp = await waitForIceGathering(pc);
  return { pc, dcChat, dcFile, sdp };
}

export async function acceptAnswer(pc, answerEncoded, passphrase) {
  const answerSdp = await decryptSDP(answerEncoded, passphrase);
  await pc.setRemoteDescription({ type: "answer", sdp: answerSdp });
}

export async function createRoomAnswer(offerSdp, onChannelsOpen) {
  const pc = createPeerConnection();
  let dcChat = null, dcFile = null;
  pc.addEventListener("datachannel", (e) => {
    if (e.channel.label === "mesh.chat") {
      dcChat = e.channel;
      whenBothOpen(dcChat, dcFile, onChannelsOpen);
    } else if (e.channel.label === "mesh.file") {
      dcFile = e.channel;
      whenBothOpen(dcChat, dcFile, onChannelsOpen);
    }
  });
  await pc.setRemoteDescription({ type: "offer", sdp: offerSdp });
  const answer = await pc.createAnswer();
  await pc.setLocalDescription(answer);
  const sdp = await waitForIceGathering(pc);
  return { pc, dcChat, dcFile, sdp };
}

export async function acceptRoomAnswer(pc, answerSdp) {
  await pc.setRemoteDescription({ type: "answer", sdp: answerSdp });
}

export { encryptSDP, decryptSDP };
