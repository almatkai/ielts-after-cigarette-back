import { readFile, writeFile, mkdir } from "node:fs/promises";
import { createHash } from "node:crypto";
const root = new URL("..", import.meta.url),
  source = new URL("node_modules/@ffmpeg/core/dist/esm/ffmpeg-core.js", root),
  dest = new URL("dist/audio/", root);
await mkdir(dest, { recursive: true });
const original = await readFile(source, "utf8");
if (!original.includes("scriptDirectory=self.location.href"))
  throw Error("Pinned FFmpeg glue changed");
// Workers supplies a precompiled Wasm module through instantiateWasm. This URL
// is only Emscripten's unused fallback script directory, never a network origin.
await writeFile(
  new URL("ffmpeg-core.mjs", dest),
  original.replace(
    "scriptDirectory=self.location.href",
    'scriptDirectory="file:///ffmpeg-core.js"',
  ),
);
await writeFile(
  new URL("ffmpeg-core.wasm", dest),
  await readFile(new URL("ffmpeg-core.wasm", source)),
);
await writeFile(
  new URL("manifest.json", dest),
  JSON.stringify(
    {
      package: "@ffmpeg/core",
      version: "0.12.10",
      glueSourceSHA256: createHash("sha256").update(original).digest("hex"),
      wasmSHA256: createHash("sha256")
        .update(await readFile(new URL("ffmpeg-core.wasm", dest)))
        .digest("hex"),
    },
    null,
    2,
  ) + "\n",
);
