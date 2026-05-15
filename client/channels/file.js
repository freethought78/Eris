const CHUNK_SIZE = 65536;

function bufToBase64(buf) {
  const bytes = new Uint8Array(buf);
  let s = "";
  for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
  return btoa(s);
}

function base64ToBuf(s) {
  const s2 = atob(s);
  const bytes = new Uint8Array(s2.length);
  for (let i = 0; i < s2.length; i++) bytes[i] = s2.charCodeAt(i);
  return bytes.buffer;
}

export function sendFile(dc, file, { onProgress }) {
  const id = crypto.randomUUID();
  const total = Math.ceil(file.size / CHUNK_SIZE);
  let chunk = 0;

  return new Promise((resolve, reject) => {
    const reader = new FileReader();

    reader.onerror = () => reject(reader.error);

    reader.onload = () => {
      const data = bufToBase64(reader.result);
      dc.send(JSON.stringify({ id, name: file.name, size: file.size, chunk, total, data }));
      chunk++;
      onProgress?.({ name: file.name, chunk, total });
      if (chunk < total) readNext();
      else resolve();
    };

    function readNext() {
      const start = chunk * CHUNK_SIZE;
      const end = Math.min(start + CHUNK_SIZE, file.size);
      reader.readAsArrayBuffer(file.slice(start, end));
    }

    readNext();
  });
}

export function receiveFile(dc, { onProgress, onComplete }) {
  const files = new Map();

  dc.addEventListener("message", (e) => {
    let msg;
    try {
      msg = JSON.parse(e.data);
    } catch { return; }
    if (typeof msg.id !== "string" || typeof msg.data !== "string") return;

    let entry = files.get(msg.id);
    if (!entry) {
      entry = { name: msg.name, size: msg.size, chunks: [], total: msg.total };
      files.set(msg.id, entry);
    }
    entry.chunks[msg.chunk] = msg.data;
    onProgress?.({ name: msg.name, chunk: msg.chunk + 1, total: msg.total });

    if (entry.chunks.length === msg.total && entry.chunks.every((c) => c !== undefined)) {
      const parts = entry.chunks.map((c) => base64ToBuf(c));
      const totalLen = parts.reduce((a, b) => a + b.byteLength, 0);
      const combined = new Uint8Array(totalLen);
      let offset = 0;
      for (const part of parts) {
        combined.set(new Uint8Array(part), offset);
        offset += part.byteLength;
      }
      const blob = new Blob([combined]);
      onComplete?.({ name: entry.name, blob, size: entry.size });
      files.delete(msg.id);
    }
  });
}
