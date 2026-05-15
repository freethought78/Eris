import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";

const ROOT = path.resolve(import.meta.dirname);
const CLIENT_DIR = path.join(ROOT, "client");
const SERVER_DIR = path.join(ROOT, "server");
const BUNDLE_DIR = path.join(ROOT, "bundle");
const OUT_ZIP = path.join(BUNDLE_DIR, "mesh.zip");

if (!fs.existsSync(BUNDLE_DIR)) fs.mkdirSync(BUNDLE_DIR, { recursive: true });

// Build a simple zip using the archiver pattern with Node built-ins.
// For a zero-dep build, we collect all files, then write them into a single temp dir.
const tmp = fs.mkdtempSync(path.join(ROOT, ".mesh-build-"));
try {
  // Copy client/
  copyDir(CLIENT_DIR, path.join(tmp, "client"));
  // Copy server/ (exclude node_modules)
  copyDir(SERVER_DIR, path.join(tmp, "server"), (name) => name !== "node_modules");
  // Zip tmp into bundle/mesh.zip
  await zipDir(tmp, OUT_ZIP);
  // Also copy into client/ for the download route
  fs.copyFileSync(OUT_ZIP, path.join(CLIENT_DIR, "mesh.zip"));
  console.log("Built bundle/mesh.zip and client/mesh.zip");
} finally {
  fs.rmSync(tmp, { recursive: true, force: true });
}

function copyDir(src, dest, filter) {
  fs.mkdirSync(dest, { recursive: true });
  for (const entry of fs.readdirSync(src, { withFileTypes: true })) {
    if (filter && !filter(entry.name)) continue;
    const s = path.join(src, entry.name);
    const d = path.join(dest, entry.name);
    if (entry.isDirectory()) {
      copyDir(s, d, filter);
    } else {
      fs.copyFileSync(s, d);
    }
  }
}

async function zipDir(srcDir, outPath) {
  // Use Node built-in zlib + a manual zip writer.
  // For simplicity, shell out to Compress-Archive on Windows or fallback.
  const { execSync } = await import("node:child_process");
  execSync(
    `powershell -NoProfile -Command "Compress-Archive -Path '${srcDir}\\*' -DestinationPath '${outPath}' -Force"`,
    { stdio: "pipe" }
  );
}
