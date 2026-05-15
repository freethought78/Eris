import { encryptSDP, decryptSDP } from "./crypto.js";

const ICE_SERVERS = [
  { urls: "stun:stun.l.google.com:19302" },
  { urls: "stun:stun1.l.google.com:19302" },
  { urls: "stun:stun.cloudflare.com:3478" },
];

export function createPeerConnection() {
  const pc = new RTCPeerConnection({ iceServers: ICE_SERVERS });
  return pc;
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

export async function createOffer(onDataChannel) {
  const pc = createPeerConnection();
  const dc = pc.createDataChannel("mesh.chat", { ordered: true });
  dc.addEventListener("open", () => onDataChannel?.(dc));
  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);
  const sdp = await waitForIceGathering(pc);
  return { pc, dc, sdp };
}

export async function createAnswer(offerEncoded, passphrase, onDataChannel) {
  const offerSdp = await decryptSDP(offerEncoded, passphrase);
  const pc = createPeerConnection();
  let dc = null;
  pc.addEventListener("datachannel", (e) => {
    dc = e.channel;
    dc.addEventListener("open", () => onDataChannel?.(dc));
  });
  await pc.setRemoteDescription({ type: "offer", sdp: offerSdp });
  const answer = await pc.createAnswer();
  await pc.setLocalDescription(answer);
  const sdp = await waitForIceGathering(pc);
  return { pc, dc, sdp };
}

export async function acceptAnswer(pc, answerEncoded, passphrase) {
  const answerSdp = await decryptSDP(answerEncoded, passphrase);
  await pc.setRemoteDescription({ type: "answer", sdp: answerSdp });
  return pc;
}

export { encryptSDP, decryptSDP };
