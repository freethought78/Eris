export async function getStream({ audio, video }) {
  try {
    return await navigator.mediaDevices.getUserMedia({ audio, video });
  } catch (err) {
    if (err.name === "NotAllowedError") throw new Error("Camera/mic permission denied");
    if (err.name === "NotFoundError") throw new Error("No camera/mic found");
    throw err;
  }
}

export function stopStream(stream) {
  if (!stream) return;
  for (const track of stream.getTracks()) {
    track.stop();
  }
}

export function addMediaToPC(pc, stream) {
  for (const track of stream.getTracks()) {
    pc.addTrack(track, stream);
  }
}

export function removeMediaFromPC(pc, stream) {
  if (!stream) return;
  for (const track of stream.getTracks()) {
    try { pc.removeTrack(pc.getSenders().find((s) => s.track === track)); } catch {}
    track.stop();
  }
}

export function createVideoElement(stream, muted) {
  const video = document.createElement("video");
  video.srcObject = stream;
  video.autoplay = true;
  video.playsInline = true;
  video.muted = !!muted;
  if (muted) video.style.transform = "scaleX(-1)";
  return video;
}
