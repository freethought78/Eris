export async function getScreenStream() {
  try {
    return await navigator.mediaDevices.getDisplayMedia({ video: true, audio: false });
  } catch (err) {
    if (err.name === "NotAllowedError" || err.name === "AbortError" || err.name === "SecurityError") return null;
    throw err;
  }
}
