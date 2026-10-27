// Platform transport only. Application-owned URLs, credentials, provider routing,
// prompts, retries and response validation remain in the pinned Go application.
const encoder = new TextEncoder(),
  decoder = new TextDecoder();
const base64 = (bytes) => {
  let s = "";
  for (let i = 0; i < bytes.length; i += 8192)
    s += String.fromCharCode(...bytes.subarray(i, i + 8192));
  return btoa(s);
};
async function audioBody(bytes, media) {
  let payload;
  try {
    payload = JSON.parse(decoder.decode(bytes));
  } catch {
    return bytes;
  }
  const references = new WeakMap();
  for (const message of payload.messages ?? [])
    if (Array.isArray(message.content))
      for (const part of message.content) {
        const data =
          part?.type === "input_audio" ? part.input_audio?.data : null;
        if (
          data &&
          typeof data === "object" &&
          Object.keys(data).length === 1 &&
          typeof data.$cfAudio === "string"
        ) {
          const object = await media.head(data.$cfAudio);
          if (!object || object.size < 1 || object.size > 12 * 1024 * 1024)
            throw Error(
              "Speaking audio is missing or exceeds the original limit",
            );
          references.set(data, { key: object.key, size: object.size });
        }
      }
  function* serialize(value) {
    if (value && typeof value === "object") {
      const reference = references.get(value);
      if (reference) {
        yield '"';
        yield reference;
        yield '"';
        return;
      }
      if (Array.isArray(value)) {
        yield "[";
        for (let i = 0; i < value.length; i++) {
          if (i) yield ",";
          yield* serialize(value[i]);
        }
        yield "]";
        return;
      }
      yield "{";
      let count = 0;
      for (const [key, item] of Object.entries(value)) {
        if (count++) yield ",";
        yield JSON.stringify(key) + ":";
        yield* serialize(item);
      }
      yield "}";
      return;
    }
    yield JSON.stringify(value);
  }
  const segments = [...serialize(payload)];
  if (!segments.some((v) => typeof v !== "string")) return bytes;
  const length = segments.reduce(
    (n, v) =>
      n +
      (typeof v === "string"
        ? encoder.encode(v).length
        : 4 * Math.ceil(v.size / 3)),
    0,
  );
  async function* stream() {
    for (const segment of segments) {
      if (typeof segment === "string") {
        yield encoder.encode(segment);
        continue;
      }
      const object = await media.get(segment.key);
      if (!object || object.size !== segment.size)
        throw Error("Speaking audio changed during assessment");
      const reader = object.body.getReader();
      let pending = new Uint8Array(0),
        count = 0;
      try {
        for (;;) {
          const { done, value } = await reader.read();
          if (done) break;
          count += value.length;
          for (let at = 0; at < value.length; at += 48 * 1024) {
            const slice = value.subarray(at, at + 48 * 1024),
              combined = new Uint8Array(pending.length + slice.length);
            combined.set(pending);
            combined.set(slice, pending.length);
            const usable = combined.length - (combined.length % 3);
            if (usable)
              yield encoder.encode(base64(combined.subarray(0, usable)));
            pending = combined.slice(usable);
          }
        }
        if (pending.length) yield encoder.encode(base64(pending));
        if (count !== segment.size)
          throw Error("Speaking audio length mismatch");
      } finally {
        await reader.cancel().catch(() => {});
        reader.releaseLock();
      }
    }
  }
  const iterator = stream(),
    source = new ReadableStream({
      async pull(controller) {
        try {
          const item = await iterator.next();
          if (item.done) controller.close();
          else controller.enqueue(item.value);
        } catch (e) {
          controller.error(e);
        }
      },
      cancel() {
        return iterator.return();
      },
    });
  const fixed = new FixedLengthStream(length);
  source.pipeTo(fixed.writable).catch(() => {});
  return fixed.readable;
}
export class NativeFetch {
  constructor(media, audio) {
    this.media = media;
    this.audio = audio;
    this.requests = new Map();
  }
  async start(input, bytes) {
    const url = new URL(input.url);
    if (
      input.protected &&
      (url.protocol !== "https:" ||
        url.username ||
        url.password ||
        (url.port && url.port !== "443") ||
        /^(localhost|127\.|10\.|192\.168\.|169\.254\.|0\.|\[::1\])/i.test(
          url.hostname,
        ) ||
        /\.(localhost|local)$/i.test(url.hostname))
    )
      throw Error("Provider destination is not public HTTPS");
    const headers = new Headers();
    for (const [key, values] of Object.entries(input.headers))
      for (const value of values) headers.append(key, value);
    const controller = new AbortController(),
      state = { controller };
    this.requests.set(input.id, state);
    try {
      let body = bytes?.length ? bytes : undefined;
      if (
        body &&
        headers.get("content-type") === "application/x-iac-native-transcription"
      ) {
        const built = await transcriptionBody(body, this.media, this.audio);
        body = built.body;
        headers.set("content-type", built.contentType);
        state.temporary = built.temporary;
      } else if (
        body &&
        headers.get("content-type")?.startsWith("application/json")
      )
        body = await audioBody(body, this.media);
      const response = await fetch(input.url, {
        method: input.method,
        headers,
        body,
        redirect: "manual",
        signal: controller.signal,
      });
      state.reader = response.body?.getReader();
      const out = {};
      for (const [key, value] of response.headers) out[key] = [value];
      if (response.headers.getSetCookie)
        out["set-cookie"] = response.headers.getSetCookie();
      return {
        id: input.id,
        status: response.status,
        headers: out,
        length: response.headers.has("content-length")
          ? Number(response.headers.get("content-length"))
          : -1,
      };
    } catch (e) {
      this.requests.delete(input.id);
      throw e;
    } finally {
      if (state.temporary) await this.media.delete(state.temporary);
    }
  }
  async read({ id, length }) {
    const state = this.requests.get(id);
    if (!state?.reader) return null;
    while (!state.pending?.length) {
      const { done, value } = await state.reader.read();
      if (done) return null;
      state.pending = value;
    }
    const bytes = state.pending.subarray(0, length);
    state.pending = state.pending.subarray(bytes.length);
    return base64(bytes);
  }
  async close(id) {
    const state = this.requests.get(id);
    this.requests.delete(id);
    state?.controller.abort();
    await state?.reader?.cancel().catch(() => {});
  }
}

async function transcriptionBody(bytes, media, audio) {
  const descriptor = JSON.parse(decoder.decode(bytes));
  let key = descriptor.key,
    filename = descriptor.filename,
    temporary;
  if (/\.(webm|ogg|m4a)$/i.test(filename)) {
    if (!audio) throw Error("Native audio binding is required");
    const result = await audio
      .get(audio.idFromName("ielts-audio-dev-v1"))
      .convert(key);
    key = result.key;
    temporary = key;
    filename = filename.replace(/\.[^.]+$/, ".wav");
  }
  const object = await media.get(key);
  if (!object) throw Error("Recording is missing");
  const boundary = "iac-" + crypto.randomUUID(),
    escape = (s) =>
      s.replaceAll("\r", " ").replaceAll("\n", " ").replaceAll('"', "%22");
  const prefix = encoder.encode(
    `--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="${escape(filename)}"\r\nContent-Type: application/octet-stream\r\n\r\n`,
  );
  let suffix = "\r\n";
  for (const [name, value] of [
    ["model", "speech-to-text"],
    ["language", "en"],
    ["response_format", "verbose_json"],
  ])
    suffix += `--${boundary}\r\nContent-Disposition: form-data; name="${name}"\r\n\r\n${value}\r\n`;
  suffix += `--${boundary}--\r\n`;
  const tail = encoder.encode(suffix),
    fixed = new FixedLengthStream(prefix.length + object.size + tail.length);
  const pump = (async () => {
    const writer = fixed.writable.getWriter();
    try {
      await writer.write(prefix);
      const reader = object.body.getReader();
      try {
        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;
          await writer.write(value);
        }
      } finally {
        reader.releaseLock();
      }
      await writer.write(tail);
      await writer.close();
    } catch (e) {
      await writer.abort(e);
    }
  })();
  pump.catch(() => {});
  return {
    body: fixed.readable,
    contentType: "multipart/form-data; boundary=" + boundary,
    temporary,
  };
}
