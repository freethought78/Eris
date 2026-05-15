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
    const hasSender = pc.getSenders().some((s) => s.track === track);
    if (!hasSender) pc.addTrack(track, stream);
  }
}

export function removeMediaFromPC(pc, stream, kind) {
  if (!stream) return;
  const tracks = kind ? stream.getTracks().filter((t) => t.kind === kind) : [...stream.getTracks()];
  for (const track of tracks) {
    const sender = pc.getSenders().find((s) => s.track === track);
    if (sender) { try { pc.removeTrack(sender); } catch {} }
    track.stop();
    stream.removeTrack(track);
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
