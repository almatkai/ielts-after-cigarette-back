import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash, randomUUID } from "node:crypto";
import { probeAudio, probeAudioState } from "../tools/audio-probe-worker.mjs";
import { wavFormat } from "../tools/audio-platform-probe.mjs";
const sha = (b) => createHash("sha256").update(b).digest();
function wav() {
  const b = Buffer.alloc(44 + 32000);
  b.write("RIFF", 0);
  b.writeUInt32LE(b.length - 8, 4);
  b.write("WAVEfmt ", 8);
  b.writeUInt32LE(16, 16);
  b.writeUInt16LE(1, 20);
  b.writeUInt16LE(1, 22);
  b.writeUInt32LE(16000, 24);
  b.writeUInt32LE(32000, 28);
  b.writeUInt16LE(2, 32);
  b.writeUInt16LE(16, 34);
  b.write("data", 36);
  b.writeUInt32LE(32000, 40);
  return b;
}
function context(temporaryFixture = false, failure = false) {
  const original = temporaryFixture
      ? "_migration-probe/audio-" + randomUUID() + ".webm"
      : "source/educational-original.mp3",
    output = "_cf-temp/audio-" + randomUUID() + ".wav",
    map = new Map(),
    deleted = [];
  function put(key, bytes, type) {
    map.set(key, {
      bytes,
      size: bytes.length,
      checksums: { sha256: sha(bytes) },
      version: "version-1",
      httpMetadata: { contentType: type },
    });
  }
  put(original, Buffer.from("original"), "audio/mpeg");
  const env = {
    MEDIA: {
      async head(key) {
        return map.get(key) ?? null;
      },
      async get(key) {
        const o = map.get(key);
        return o ? { ...o, body: new Response(o.bytes).body } : null;
      },
      async delete(key) {
        deleted.push(key);
        map.delete(key);
      },
    },
    IAC_AUDIO: {
      idFromName(name) {
        assert.equal(name, "ielts-audio-dev-v1");
        return name;
      },
      get() {
        return {
          async convert(key) {
            assert.equal(key, original);
            if (failure) throw Error("Synthetic decoder failure");
            put(output, wav(), "audio/wav");
            return {
              key: output,
              size: wav().length,
              memory: 32 * 1024 * 1024,
            };
          },
        };
      },
    },
  };
  const input = {
    key: original,
    size: 8,
    sha256: sha(Buffer.from("original")).toString("hex"),
    temporaryFixture,
  };
  return {
    env,
    input,
    original,
    output,
    map,
    deleted,
    request: () =>
      new Request("http://127.0.0.1:8794/probe/audio", {
        method: "POST",
        body: JSON.stringify(input),
      }),
  };
}
test("private audio probe streams verified bytes and removes only its derived output", async () => {
  const c = context(),
    response = await probeAudio(c.request(), c.env);
  assert.equal(response.headers.get("x-audio-source-preserved"), "true");
  assert.equal(response.headers.get("x-audio-output-bytes"), "32044");
  const bytes = Buffer.from(await response.arrayBuffer());
  assert.equal(
    sha(bytes).toString("hex"),
    response.headers.get("x-audio-sha256"),
  );
  assert.equal(wavFormat(bytes).sampleRate, 16000);
  assert.deepEqual(c.deleted, [c.output]);
  assert.ok(c.map.has(c.original));
  const state = await probeAudioState(
    new Request("http://test", {
      method: "POST",
      body: JSON.stringify({ keys: [c.output] }),
    }),
    c.env,
  );
  assert.deepEqual(await state.json(), { remaining: 0 });
});
test("synthetic fixture cleanup is explicit, scoped and runs on body cancellation", async () => {
  const c = context(true),
    response = await probeAudio(c.request(), c.env);
  await response.body.cancel();
  assert.deepEqual(c.deleted, [c.output, c.original]);
  assert.equal(c.map.size, 0);
});
test("decoder failure preserves real original; only explicitly temporary fixture is removed", async () => {
  for (const temporaryFixture of [false, true]) {
    const c = context(temporaryFixture, true);
    await assert.rejects(probeAudio(c.request(), c.env));
    assert.deepEqual(c.deleted, temporaryFixture ? [c.original] : []);
    assert.equal(c.map.has(c.original), !temporaryFixture);
  }
});
test("wrong input checksum and unscoped fixture/delete keys cannot remove an original", async () => {
  const c = context();
  c.input.sha256 = "0".repeat(64);
  await assert.rejects(probeAudio(c.request(), c.env));
  assert.deepEqual(c.deleted, []);
  c.input.temporaryFixture = true;
  await assert.rejects(probeAudio(c.request(), c.env));
  assert.deepEqual(c.deleted, []);
  await assert.rejects(
    probeAudioState(
      new Request("http://test", {
        method: "POST",
        body: JSON.stringify({ keys: [c.original] }),
      }),
      c.env,
    ),
  );
});
test("bounded WAV parser accepts PCM mono16k and rejects wrong sample format or truncated header", () => {
  assert.deepEqual(wavFormat(wav()), {
    encoding: 1,
    channels: 1,
    sampleRate: 16000,
    bits: 16,
    dataOffset: 44,
  });
  for (const [offset, value] of [
    [20, 3],
    [22, 2],
    [34, 8],
  ]) {
    const bad = wav();
    bad.writeUInt16LE(value, offset);
    assert.throws(() => wavFormat(bad));
  }
  assert.throws(() => wavFormat(Buffer.alloc(12)));
  assert.throws(() => wavFormat(wav().subarray(0, 20)));
});
