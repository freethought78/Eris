export function createTextChannel(dc, { onMessage }) {
  if (!dc) throw new Error("DataChannel required");

  dc.addEventListener("message", (e) => {
    try {
      const msg = JSON.parse(e.data);
      if (msg.type === "text") {
        onMessage?.(msg);
      }
    } catch {
      // ignore malformed messages
    }
  });

  return {
    send(text, from) {
      const msg = { type: "text", from, text, ts: Date.now() };
      dc.send(JSON.stringify(msg));
    },
  };
}
