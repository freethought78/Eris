// In-memory room store. Never written to disk.
const rooms = new Map();

function send(ws, obj) {
  if (ws.readyState === 1) ws.send(JSON.stringify(obj));
}

export function createRoom(name, passwordHash, ws) {
  if (rooms.has(name)) {
    send(ws, { type: "error", message: "Room name already taken" });
    return;
  }
  rooms.set(name, { passwordHash, creator: ws, joiner: null });
  send(ws, { type: "created", room: name });
}

export function joinRoom(name, passwordHash, ws) {
  const room = rooms.get(name);
  if (!room) {
    send(ws, { type: "error", message: "Room not found" });
    return;
  }
  if (room.joiner) {
    send(ws, { type: "error", message: "Room is full" });
    return;
  }
  if (room.passwordHash !== passwordHash) {
    send(ws, { type: "error", message: "Wrong password" });
    return;
  }
  room.joiner = ws;
  send(ws, { type: "joined", room: name });
  send(room.creator, { type: "peer-joined", room: name });
}

export function signal(ws, data, name) {
  const room = rooms.get(name);
  if (!room) return;
  const target = ws === room.creator ? room.joiner : room.creator;
  if (target) send(target, { type: "signal", room: name, data });
}

export function removeConnection(ws) {
  for (const [name, room] of rooms) {
    if (room.creator === ws) {
      if (room.joiner) {
        send(room.joiner, { type: "peer-left", room: name });
      }
      rooms.delete(name);
    } else if (room.joiner === ws) {
      send(room.creator, { type: "peer-left", room: name });
      rooms.delete(name);
    }
  }
}
