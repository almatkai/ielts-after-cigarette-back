// Temporary private-helper acceptance tool. Never bind these handlers publicly.
import { createHash } from "node:crypto";
import { readFile, writeFile, stat } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { bindingLocalOrigin } from "./transfer.mjs";
export function wavFormat(header) {
  const bytes = new Uint8Array(header),
    view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength),
    text = (at, n) => new TextDecoder().decode(bytes.subarray(at, at + n));
  if (text(0, 4) !== "RIFF" || text(8, 4) !== "WAVE")
    throw Error("Not RIFF/WAVE");
  let format;
  for (let at = 12; at + 8 <= bytes.length;) {
    const kind = text(at, 4),
      size = view.getUint32(at + 4, true),
      body = at + 8;
    if (kind === "fmt ") {
      if (size < 16 || body + 16 > bytes.length)
        throw Error("Invalid WAV format");
      format = {
        encoding: view.getUint16(body, true),
        channels: view.getUint16(body + 2, true),
        sampleRate: view.getUint32(body + 4, true),
        bits: view.getUint16(body + 14, true),
      };
    }
    if (kind === "data") {
      if (
        !format ||
        format.encoding !== 1 ||
        format.channels !== 1 ||
        format.sampleRate !== 16000 ||
        format.bits !== 16
      )
        throw Error("Unexpected PCM format");
      return { ...format, dataOffset: body };
    }
    at = body + size + (size % 2);
  }
  throw Error("No bounded WAV data header");
}
async function main() {
  const args = process.argv.slice(2),
    get = (flag) => args[args.indexOf(flag) + 1];
  if (
    !args.includes("--execute") ||
    !args.includes("--input") ||
    !args.includes("--report")
  )
    throw Error("Explicit private probe input/report and --execute required");
  const endpoint = bindingLocalOrigin([
      "--binding-local",
      "--endpoint",
      get("--endpoint"),
    ]),
    inputPath = get("--input"),
    reportPath = get("--report");
  if ((await stat(inputPath)).mode & 0o077)
    throw Error("Private probe input must have mode 600");
  const input = JSON.parse(await readFile(inputPath, "utf8")),
    start = Date.now();
  const response = await fetch(new URL("/probe/audio", endpoint), {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(input),
    redirect: "error",
  });
  if (!response.ok) throw Error("Native audio probe rejected");
  let count = 0,
    header = new Uint8Array(0),
    nonzero = false;
  const hash = createHash("sha256");
  for await (const chunk of response.body) {
    count += chunk.length;
    hash.update(chunk);
    if (header.length < 4096) {
      const next = new Uint8Array(Math.min(4096, header.length + chunk.length));
      next.set(header);
      next.set(chunk.subarray(0, next.length - header.length), header.length);
      header = next;
    }
    for (let i = 512; i < chunk.length; i += 512) if (chunk[i]) nonzero = true;
  }
  const format = wavFormat(header),
    duration = (count - format.dataOffset) / 32000;
  const actualDigest = hash.digest("hex");
  console.log(
    JSON.stringify({
      probeValidation: {
        bytes: count,
        declaredBytes: Number(response.headers.get("x-audio-output-bytes")),
        checksumMatches:
          actualDigest === response.headers.get("x-audio-sha256"),
        sourcePreserved: response.headers.get("x-audio-source-preserved"),
        nonzero,
        duration,
        expectedDuration: input.expectedDuration,
        format,
      },
    }),
  );
  if (
    count !== Number(response.headers.get("x-audio-output-bytes")) ||
    actualDigest !== response.headers.get("x-audio-sha256") ||
    response.headers.get("x-audio-source-preserved") !== "true" ||
    (input.requireNonzero && !nonzero) ||
    Math.abs(duration - input.expectedDuration) > 0.15
  )
    throw Error(
      "Native output checksum, duration, size or source-preservation assertion failed",
    );
  const keys = [response.headers.get("x-audio-derived-key")];
  if (input.temporaryFixture) keys.push(input.key);
  const state = await fetch(new URL("/probe/audio-state", endpoint), {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ keys }),
    redirect: "error",
  });
  if (!state.ok || (await state.json()).remaining !== 0)
    throw Error("Probe temporary cleanup failed");
  const report = {
    nativeCloudflare: true,
    inputBytes: input.size,
    outputBytes: count,
    pcmBytes: count - format.dataOffset,
    durationSeconds: duration,
    format,
    nonzeroPCM: nonzero,
    wasmMemoryBytes: Number(response.headers.get("x-audio-wasm-memory")),
    checksumVerified: true,
    sourcePreserved: true,
    temporaryObjectsRemoved: true,
    totalMemoryPeakMeasured: false,
    elapsedMs: Date.now() - start,
  };
  await writeFile(reportPath, JSON.stringify(report, null, 2) + "\n", {
    flag: "wx",
  });
  console.log(JSON.stringify(report));
}
if (process.argv[1] === fileURLToPath(import.meta.url))
  main().catch(() => {
    console.error(
      "Native audio acceptance failed; inspect metadata-only tails, never log private keys or records.",
    );
    process.exitCode = 1;
  });
