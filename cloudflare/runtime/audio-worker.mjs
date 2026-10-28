import { mediaEnvironment } from "./blob-store.mjs";
import { DurableObject } from "cloudflare:workers";
import createCore from "../dist/audio/ffmpeg-core.mjs";
import wasm from "../dist/audio/ffmpeg-core.wasm";

export default {
  fetch() {
    return new Response(null, { status: 404 });
  },
};
export class IACAudio extends DurableObject {
  constructor(ctx, env) {
    super(ctx, env);
    this.env = mediaEnvironment(env);
    this.tail = Promise.resolve();
    ctx.storage.sql.exec(
      "CREATE TABLE IF NOT EXISTS audio_chunks(position INTEGER PRIMARY KEY, bytes BLOB NOT NULL)",
    );
  }
  convert(key) {
    const job = this.tail.then(() => this.transcode(key));
    this.tail = job.catch(() => {});
    return job;
  }
  async core() {
    if (!this.instance)
      this.instance = await createCore({
        instantiateWasm(imports, receive) {
          const instance = new WebAssembly.Instance(wasm, imports);
          receive(instance, wasm);
          return instance.exports;
        },
      });
    return this.instance;
  }
  async transcode(key) {
    const object = await this.env.MEDIA.get(key);
    if (!object || object.size < 1 || object.size > 12 * 1024 * 1024)
      throw Error("Compressed recording exceeds original size limit");
    const core = await this.core(),
      sql = this.ctx.storage.sql;
    const name = "/recording",
      output = "/converted.wav",
      target = "_cf-temp/audio-" + crypto.randomUUID() + ".wav";
    // Only derived scratch chunks live here. A fresh attempt also removes any
    // incomplete capture left by an object reset, without touching source R2.
    sql.exec("DELETE FROM audio_chunks");
    core.FS.writeFile(name, new Uint8Array(await object.arrayBuffer()));
    let capture = new Uint8Array(1024 * 1024),
      captured = 0,
      position = 0,
      total = 0;
    const flush = () => {
      if (captured) {
        sql.exec(
          "INSERT INTO audio_chunks VALUES(?,?)",
          position++,
          capture.slice(0, captured).buffer,
        );
        captured = 0;
      }
    };
    const device = core.FS.makedev(64, 0);
    core.FS.registerDevice(device, {
      open(stream) {
        stream.seekable = false;
      },
      write(stream, bytes, offset, length) {
        let read = 0;
        while (read < length) {
          const count = Math.min(capture.length - captured, length - read);
          capture.set(
            bytes.subarray(offset + read, offset + read + count),
            captured,
          );
          captured += count;
          read += count;
          total += count;
          if (captured === capture.length) flush();
        }
        return length;
      },
    });
    core.FS.mkdev(output, 0o666, device);
    let upload,
      completed = false;
    try {
      // A single decoder/resampler pass preserves continuous audio and codec
      // delay. The unseekable device emits the same streaming WAV as pipe:1 in
      // the selected application. Synchronous SQLite writes avoid buffering
      // the expanded waveform in Wasm/JS or making synchronous network calls.
      core.reset();
      if (
        core.exec(
          "-v",
          "error",
          "-i",
          name,
          "-f",
          "wav",
          "-ar",
          "16000",
          "-ac",
          "1",
          output,
        ) !== 0
      )
        throw Error("Compressed recording could not be decoded");
      flush();
      if (!total) throw Error("Recording produced no acoustic samples");
      capture = null;
      upload = await this.env.MEDIA.createMultipartUpload(target, {
        httpMetadata: { contentType: "audio/wav" },
      });
      const parts = [];
      let buffer = new Uint8Array(5 * 1024 * 1024),
        used = 0;
      let nextChunk = 0;
      for (;;) {
        const row = sql
          .exec(
            "SELECT position,bytes FROM audio_chunks WHERE position>=? ORDER BY position LIMIT 1",
            nextChunk,
          )
          .toArray()[0];
        if (!row) break;
        nextChunk = row.position + 1;
        const bytes = new Uint8Array(row.bytes);
        for (let offset = 0; offset < bytes.length;) {
          const count = Math.min(buffer.length - used, bytes.length - offset);
          buffer.set(bytes.subarray(offset, offset + count), used);
          offset += count;
          used += count;
          if (used === buffer.length) {
            parts.push(await upload.uploadPart(parts.length + 1, buffer));
            buffer = new Uint8Array(5 * 1024 * 1024);
            used = 0;
          }
        }
      }
      if (used)
        parts.push(
          await upload.uploadPart(parts.length + 1, buffer.subarray(0, used)),
        );
      await upload.complete(parts);
      completed = true;
      return {
        key: target,
        size: total,
        memory: core.HEAPU8.buffer.byteLength,
      };
    } finally {
      try {
        core.FS.unlink(name);
      } catch {}
      try {
        core.FS.unlink(output);
      } catch {}
      sql.exec("DELETE FROM audio_chunks");
      if (upload && !completed) await upload.abort();
    }
  }
}
