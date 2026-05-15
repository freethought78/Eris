// Unit test: cancelling a transfer actually stops the FileReader
//
// The bug: when a large file transfer is cancelled, the UI shows cancelled but the
// FileReader keeps looping forever in the background. This blocks new transfers because
// `outgoing` is never properly cleaned up (callback exceptions skip `outgoing = null`).
//
// Fix:
//   1. Every callback invocation (onProgress, onComplete, onCancelled) is wrapped in
//      try/catch so a throw never skips the cleanup lines that follow.
//   2. The guard in reader.onload checks `outgoing.id !== id` so a stale reader from
//      a cancelled transfer is always caught even if `outgoing` has been replaced by
//      a new transfer.

const CHUNK_SIZE = 65536;
let passed = 0;
let failed = 0;

// ── Deterministic async read controller ──

class ReadController {
  constructor() {
    this._queue = [];
  }
  schedule(fn) {
    this._queue.push(fn);
  }
  step() {
    if (this._queue.length === 0) return false;
    const fn = this._queue.shift();
    fn();
    return true;
  }
}

// ── Simulated environment ──

let outgoing = null;
let incoming = null;
const sent = [];

const fileDC = {
  send(msg) {
    const obj = JSON.parse(msg);
    sent.push({ id: obj.id, chunk: obj.chunk, total: obj.total });
  }
};

let onProgressCalled = false;
let onCompleteCalled = false;
let onCancelledCalled = false;

function resetCallbacks() {
  onProgressCalled = false;
  onCompleteCalled = false;
  onCancelledCalled = false;
}

const callbacks = {
  onProgress: (p) => { onProgressCalled = true; },
  onComplete: (p) => { onCompleteCalled = true; },
  onError: (p) => {},
  onCancelled: (p) => { onCancelledCalled = true; }
};

function cancelTransfer() {
  if (outgoing && !outgoing.aborted) {
    outgoing.aborted = true;
    try { callbacks.onCancelled?.({ name: outgoing.file.name, direction: "send" }); } catch {}
    outgoing = null;
  }
  if (incoming && !incoming.aborted) {
    incoming.aborted = true;
    try { callbacks.onCancelled?.({ name: incoming.name, direction: "receive" }); } catch {}
    incoming = null;
  }
}

// ── doSend — exact copy of the real logic ──

function doSend(id, file, ctrl) {
  const total = Math.ceil(file.size / CHUNK_SIZE);
  let chunk = 0;

  return new Promise((resolve) => {
    function onChunkReady() {
      if (!outgoing || outgoing.id !== id || outgoing.aborted) {
        if (outgoing && outgoing.id === id) {
          outgoing = null;
        }
        resolve();
        return;
      }
      fileDC.send(JSON.stringify({ id, name: file.name, size: file.size, chunk, total, data: "fake" }));
      chunk++;
      try { callbacks.onProgress?.({ name: file.name, chunk, total, direction: "send" }); } catch {}
      if (chunk < total) {
        ctrl.schedule(onChunkReady);
      } else {
        try { callbacks.onComplete?.({ name: file.name, direction: "send" }); } catch {}
        outgoing = null;
        resolve();
      }
    }
    ctrl.schedule(onChunkReady);
  });
}

// ── Helpers ──

function makeMockFile(name, size) {
  return { name, size, slice(s, e) { return makeMockFile(name, e - s); } };
}

function reset() {
  outgoing = null;
  incoming = null;
  sent.length = 0;
  resetCallbacks();
}

async function test(name, fn) {
  process.stdout.write(`  [${name}] `);
  reset();
  try {
    await fn();
    process.stdout.write("PASS\n"); passed++;
  } catch (err) {
    process.stdout.write(`FAIL (${err.message})\n`); failed++;
  }
}

// ── Tests ──

async function run() {
  process.stdout.write("\n=== File Transfer Cancel + Stale Reader ===\n\n");

  // Test 1: Cancel a multi-chunk transfer mid-flight.
  // After cancel, no more chunks from that transfer should be sent.
  await test("Cancel stops reader: no chunks after cancel", async () => {
    const ctrl = new ReadController();

    // 5 chunks (large file)
    outgoing = { id: "A", file: makeMockFile("a.txt", CHUNK_SIZE * 4 + 1), aborted: false };
    const promiseA = doSend("A", outgoing.file, ctrl);

    ctrl.step(); // chunk 0 sent, chunk 1 queued
    ctrl.step(); // chunk 1 sent, chunk 2 queued

    if (sent.length !== 2) throw new Error("Expected 2 chunks before cancel");

    // Cancel mid-flight
    cancelTransfer(); // outgoing = null

    // Stale A1 and A2 reads may be queued; process them
    while (ctrl.step()) {}

    // Wait for promiseA (it should resolve from guard)
    await promiseA;

    // No more A chunks should have been sent after cancel
    const aAfter = sent.filter(c => c.id === "A");
    if (aAfter.length !== 2) {
      throw new Error(`A leaked ${aAfter.length - 2} chunk(s) after cancel`);
    }
    // outgoing should be null
    if (outgoing !== null) {
      throw new Error("outgoing not nulled after cancel");
    }
  });

  // Test 2: After cancel, a new transfer works
  await test("Cancel then new transfer succeeds", async () => {
    const ctrl = new ReadController();

    // Transfer A: 3 chunks
    outgoing = { id: "A", file: makeMockFile("a.txt", CHUNK_SIZE * 2 + 1), aborted: false };
    const promiseA = doSend("A", outgoing.file, ctrl);

    ctrl.step(); // A0 sent, A1 queued

    // Cancel
    cancelTransfer(); // outgoing = null
    while (ctrl.step()) {} // drain stale reads
    await promiseA;

    // Transfer B: 3 chunks
    outgoing = { id: "B", file: makeMockFile("b.txt", CHUNK_SIZE * 2 + 1), aborted: false };
    const promiseB = doSend("B", outgoing.file, ctrl);

    ctrl.step(); // B0
    ctrl.step(); // B1
    ctrl.step(); // B2
    await promiseB;

    const bChunks = sent.filter(c => c.id === "B");
    if (bChunks.length !== 3) {
      throw new Error(`Expected 3 B chunks, got ${bChunks.length}`);
    }
    // No A chunks after cancel
    const aAfter = sent.filter(c => c.id === "A" && c.chunk > 0);
    if (aAfter.length > 0) {
      throw new Error(`A leaked ${aAfter.length} chunk(s) after cancel`);
    }
    if (outgoing !== null) {
      throw new Error("outgoing not nulled after transfer complete");
    }
  });

  // Test 3: Cancel replaces outgoing → stale reader is caught by id check
  await test("Stale reader caught after cancel+replace", async () => {
    const ctrl = new ReadController();

    outgoing = { id: "A", file: makeMockFile("a.txt", CHUNK_SIZE * 3 + 1), aborted: false };
    const promiseA = doSend("A", outgoing.file, ctrl);

    ctrl.step(); // A0

    cancelTransfer(); // outgoing = null
    outgoing = { id: "B", file: makeMockFile("b.txt", CHUNK_SIZE + 1), aborted: false };
    const promiseB = doSend("B", outgoing.file, ctrl);

    // Drain: stale A reads + B reads
    while (ctrl.step()) {}

    await promiseA;
    await promiseB;

    // Only B chunks should remain (A may have A0 before cancel, that's fine)
    const aAfter = sent.filter(c => c.id === "A" && c.chunk > 0);
    if (aAfter.length > 0) {
      throw new Error(`A leaked ${aAfter.length} stale chunk(s) after replace`);
    }
    const bChunks = sent.filter(c => c.id === "B");
    if (bChunks.length !== 2) {
      throw new Error(`Expected 2 B chunks, got ${bChunks.length}`);
    }
  });

  // Test 4: Callback exception does not prevent outgoing cleanup
  await test("Callback throw does not block cleanup", async () => {
    const ctrl = new ReadController();

    // Make onComplete throw
    const origComplete = callbacks.onComplete;
    callbacks.onComplete = () => { throw new Error("onComplete kaboom"); };

    outgoing = { id: "C", file: makeMockFile("c.txt", CHUNK_SIZE + 1), aborted: false };
    const promiseC = doSend("C", outgoing.file, ctrl);

    ctrl.step(); // C0
    ctrl.step(); // C1 (last chunk → onComplete throws)

    await promiseC;

    // outgoing should still be nulled despite the throw
    if (outgoing !== null) {
      throw new Error("outgoing was NOT nulled after onComplete throw");
    }

    callbacks.onComplete = origComplete;
  });

  // Test 5: cancelTransfer callback throw does not block cleanup
  await test("cancelTransfer callback throw keeps outgoing nulled", async () => {
    const ctrl = new ReadController();

    // Make onCancelled throw
    const origCancelled = callbacks.onCancelled;
    callbacks.onCancelled = () => { throw new Error("onCancelled kaboom"); };

    outgoing = { id: "D", file: makeMockFile("d.txt", CHUNK_SIZE * 2 + 1), aborted: false };
    const promiseD = doSend("D", outgoing.file, ctrl);

    ctrl.step(); // D0

    cancelTransfer(); // onCancelled will throw, but outgoing should still be nulled

    if (outgoing !== null) {
      throw new Error("outgoing NOT nulled after cancelling with throwing callback");
    }

    while (ctrl.step()) {}
    await promiseD;

    callbacks.onCancelled = origCancelled;
  });

  // ── Results ──
  const total = passed + failed;
  process.stdout.write(`\n=== Results: ${passed}/${total} passed, ${failed} failed ===\n`);
  process.exit(failed === 0 ? 0 : 1);
}

run().catch((err) => {
  process.stdout.write(`\nTest suite error: ${err.message}\n`);
  process.exit(1);
});
