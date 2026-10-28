// TEMPORARY private-helper probe handlers; never bind them publicly.
const hex = (bytes) =>
  [...new Uint8Array(bytes)]
    .map((n) => n.toString(16).padStart(2, "0"))
    .join("");
const derived = (key) => /^_cf-temp\/audio-[0-9a-f-]{36}\.wav$/.test(key);
const fixture = (key) =>
  /^_migration-probe\/audio-[0-9a-f-]{36}\.webm$/.test(key);
export async function probeAudio(request, env) {
  const input = await request.json();
  if (
    typeof input.key !== "string" ||
    !Number.isSafeInteger(input.size) ||
    input.size < 1 ||
    input.size > 12 * 1024 * 1024 ||
    !/^[0-9a-f]{64}$/.test(input.sha256) ||
    (input.temporaryFixture && !fixture(input.key))
  )
    throw Error("Invalid private audio probe input");
  const before = await env.MEDIA.head(input.key);
  if (
    !before ||
    before.size !== input.size ||
    hex(before.checksums.sha256) !== input.sha256
  )
    throw Error("Audio probe input differs from verified fixture");
  let output, reader, cleanupPromise;
  const cleanup = () =>
    (cleanupPromise ??= (async () => {
      if (output && derived(output.key) && output.key !== input.key)
        await env.MEDIA.delete(output.key);
      if (input.temporaryFixture) await env.MEDIA.delete(input.key);
    })());
  try {
    const ns = env.IAC_AUDIO;
    output = await ns
      .get(ns.idFromName("ielts-audio-dev-v1"))
      .convert(input.key);
    if (!derived(output.key) || output.key === input.key)
      throw Error("Invalid derived probe output");
    const after = await env.MEDIA.head(input.key),
      object = await env.MEDIA.get(output.key);
    if (
      !after ||
      after.size !== before.size ||
      hex(after.checksums.sha256) !== input.sha256 ||
      after.version !== before.version ||
      after.httpMetadata?.contentType !== before.httpMetadata?.contentType
    )
      throw Error("Original audio changed during conversion");
    if (
      !object ||
      object.size !== output.size ||
      object.httpMetadata?.contentType !== "audio/wav"
    )
      throw Error("Invalid derived audio object");
    reader = object.body.getReader();
    // Native HTTP forwards bytes; verification/hash work runs in the local CLI,
    // leaving the Free ingress CPU budget to metadata and bounded stream forwarding.
    const stream = new ReadableStream({
      async pull(controller) {
        try {
          const next = await reader.read();
          if (next.done) {
            await cleanup();
            controller.close();
          } else controller.enqueue(next.value);
        } catch (error) {
          await reader.cancel().catch(() => {});
          await cleanup();
          controller.error(error);
        }
      },
      async cancel() {
        await reader.cancel().catch(() => {});
        await cleanup();
      },
    });
    return new Response(stream, {
      headers: {
        "content-type": "audio/wav",
        "content-length": String(output.size),
        "x-audio-input-bytes": String(input.size),
        "x-audio-output-bytes": String(output.size),
        "x-audio-wasm-memory": String(output.memory),
        "x-audio-sha256": hex(object.checksums.sha256),
        "x-audio-derived-key": output.key,
        "x-audio-source-preserved": "true",
      },
    });
  } catch (error) {
    await cleanup();
    throw error;
  }
}
export async function probeAudioState(request, env) {
  const { keys } = await request.json();
  if (
    !Array.isArray(keys) ||
    keys.length > 2 ||
    keys.some((key) => !derived(key) && !fixture(key))
  )
    throw Error("Invalid audio probe cleanup keys");
  let remaining = 0;
  for (const key of keys) if (await env.MEDIA.head(key)) remaining++;
  return Response.json({ remaining });
}
