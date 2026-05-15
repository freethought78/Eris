import http from "node:http";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { WebSocketServer } from "ws";

const ROOT = path.resolve(import.meta.dirname, "..");
const CLIENT_DIR = path.join(ROOT, "client");
function arg(name) {
  const eq = process.argv.find(a => a.startsWith(`${name}=`));
  if (eq) return eq.split("=")[1];
  const idx = process.argv.indexOf(name);
  if (idx !== -1 && idx + 1 < process.argv.length) return process.argv[idx + 1];
  return null;
}
const PORT = parseInt(arg("--port"), 10) || 8080;
const HOST = arg("--host") || "0.0.0.0";

const MIME = {
  ".html": "text/html",
  ".js": "application/javascript",
  ".css": "text/css",
  ".png": "image/png",
  ".svg": "image/svg+xml",
  ".zip": "application/zip",
};

const server = http.createServer((req, res) => {
  const url = new URL(req.url, `http://${req.headers.host}`);

  // Download route
  if (url.pathname === "/download/mesh.zip") {
    const zipPath = path.join(CLIENT_DIR, "mesh.zip");
    if (!fs.existsSync(zipPath)) {
      res.writeHead(404);
      res.end("mesh.zip not found. Run build.js first.");
      return;
    }
    res.writeHead(200, { "Content-Type": "application/zip", "Content-Disposition": 'attachment; filename="mesh.zip"' });
    fs.createReadStream(zipPath).pipe(res);
    return;
  }

  // Static files
  let filePath = path.join(CLIENT_DIR, url.pathname === "/" ? "index.html" : url.pathname);
  const ext = path.extname(filePath);

  if (!fs.existsSync(filePath)) {
    res.writeHead(404);
    res.end("Not found");
    return;
  }

  const stat = fs.statSync(filePath);
  if (stat.isDirectory()) {
    filePath = path.join(filePath, "index.html");
    if (!fs.existsSync(filePath)) {
      res.writeHead(404);
      res.end("Not found");
      return;
    }
  }

  res.writeHead(200, { "Content-Type": MIME[ext] || "application/octet-stream" });
  fs.createReadStream(filePath).pipe(res);
});

// WebSocket /signal
const wss = new WebSocketServer({ noServer: true });

server.on("upgrade", (req, socket, head) => {
  const url = new URL(req.url, `http://${req.headers.host}`);
  if (url.pathname === "/signal") {
    wss.handleUpgrade(req, socket, head, (ws) => {
      wss.emit("connection", ws, req);
    });
  } else {
    socket.destroy();
  }
});

wss.on("connection", (ws) => {
  // Phase 1: accept connections, no room logic yet
  ws.on("message", (data) => {
    // Room forwarding will be added in Phase 3
  });
});

server.listen(PORT, HOST, () => {
  const local = `http://localhost:${PORT}`;
  const lan = getLanIP(PORT);
  console.log(`Mesh server running at ${local}`);
  if (lan) console.log(`LAN: ${lan}`);
});

function getLanIP(port) {
  const nets = os.networkInterfaces();
  for (const name of Object.keys(nets)) {
    for (const net of nets[name]) {
      if (net.family === "IPv4" && !net.internal) {
        return `http://${net.address}:${port}`;
      }
    }
  }
  return null;
}
