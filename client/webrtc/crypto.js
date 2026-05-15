const VERSION_UNENCRYPTED = "1";
const VERSION_ENCRYPTED = "2";
const PBKDF2_ITERATIONS = 100000;
const SALT_LENGTH = 16;
const IV_LENGTH = 12;

function btoa(buf) {
  const bytes = new Uint8Array(buf);
  let s = "";
  for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
  return self.btoa(s);
}

function atob(s) {
  const s2 = self.atob(s);
  const bytes = new Uint8Array(s2.length);
  for (let i = 0; i < s2.length; i++) bytes[i] = s2.charCodeAt(i);
  return bytes.buffer;
}

async function deriveKey(passphrase, salt) {
  const enc = new TextEncoder();
  const keyMaterial = await crypto.subtle.importKey(
    "raw", enc.encode(passphrase), "PBKDF2", false, ["deriveKey"]
  );
  return crypto.subtle.deriveKey(
    { name: "PBKDF2", salt, iterations: PBKDF2_ITERATIONS, hash: "SHA-256" },
    keyMaterial,
    { name: "AES-GCM", length: 256 },
    false,
    ["encrypt", "decrypt"]
  );
}

export async function encryptSDP(sdp, passphrase) {
  const enc = new TextEncoder();
  if (!passphrase) {
    return VERSION_UNENCRYPTED + ":" + btoa(enc.encode(sdp));
  }
  const salt = crypto.getRandomValues(new Uint8Array(SALT_LENGTH));
  const iv = crypto.getRandomValues(new Uint8Array(IV_LENGTH));
  const key = await deriveKey(passphrase, salt);
  const ciphertext = await crypto.subtle.encrypt(
    { name: "AES-GCM", iv }, key, enc.encode(sdp)
  );
  const combined = new Uint8Array(salt.length + iv.length + ciphertext.byteLength);
  combined.set(salt, 0);
  combined.set(iv, salt.length);
  combined.set(new Uint8Array(ciphertext), salt.length + iv.length);
  return VERSION_ENCRYPTED + ":" + btoa(combined);
}

export async function decryptSDP(encoded, passphrase) {
  const colon = encoded.indexOf(":");
  if (colon === -1) throw new Error("Invalid SDP format");
  const version = encoded.slice(0, colon);
  const data = encoded.slice(colon + 1);

  if (version === VERSION_UNENCRYPTED) {
    const dec = new TextDecoder();
    return dec.decode(atob(data));
  }

  if (version === VERSION_ENCRYPTED) {
    if (!passphrase) throw new Error("Passphrase required to decrypt");
    const raw = new Uint8Array(atob(data));
    const salt = raw.subarray(0, SALT_LENGTH);
    const iv = raw.subarray(SALT_LENGTH, SALT_LENGTH + IV_LENGTH);
    const ciphertext = raw.subarray(SALT_LENGTH + IV_LENGTH);
    const key = await deriveKey(passphrase, salt);
    const plain = await crypto.subtle.decrypt(
      { name: "AES-GCM", iv }, key, ciphertext
    );
    return new TextDecoder().decode(plain);
  }

  throw new Error(`Unknown SDP version: ${version}`);
}
