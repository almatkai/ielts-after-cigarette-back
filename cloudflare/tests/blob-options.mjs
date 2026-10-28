import { execFileSync } from "node:child_process";
export const blobBindings = {
  IAC_BLOB_INDEX: {
    className: "IACBlobIndex",
    scriptName: "ielts-blob-dev",
    useSQLite: true,
  },
  IAC_BLOB_CHUNKS: {
    className: "IACBlobChunks",
    scriptName: "ielts-blob-dev",
    useSQLite: true,
  },
};
export function blobWorker(output) {
  execFileSync(
    process.execPath,
    [
      "node_modules/wrangler/bin/wrangler.js",
      "deploy",
      "--dry-run",
      "--config",
      "wrangler.blob.jsonc",
      "--outdir",
      output,
      "tests/blob-entry.mjs",
    ],
    {
      stdio: "pipe",
      env: {
        ...process.env,
        WRANGLER_SEND_METRICS: "false",
        CLOUDFLARE_LOAD_DEV_VARS_FROM_DOT_ENV: "false",
      },
    },
  );
  return {
    name: "ielts-blob-dev",
    modules: [{ type: "ESModule", path: output + "/blob-entry.js" }],
    modulesRoot: output,
    compatibilityDate: "2026-10-19",
    durableObjects: {
      IAC_BLOB_INDEX: { className: "IACBlobIndex", useSQLite: true },
      IAC_BLOB_CHUNKS: { className: "IACBlobChunks", useSQLite: true },
    },
  };
}
export async function testBucket(mf) {
  const worker = await mf.getWorker("ielts-blob-dev");
  return {
    async put(key, body, options = {}) {
      const response = await worker.fetch(
        "https://blob.test/put?key=" +
          encodeURIComponent(key) +
          (options.sha256 ? "&sha256=" + options.sha256 : ""),
        {
          method: "POST",
          body,
          headers: {
            "content-type":
              options.httpMetadata?.contentType ?? "application/octet-stream",
          },
        },
      );
      if (!response.ok) throw Error(await response.text());
      return response.json();
    },
    async get(key) {
      const response = await worker.fetch(
        "https://blob.test/get?key=" + encodeURIComponent(key),
      );
      return response.status === 404
        ? null
        : {
            body: response.body,
            arrayBuffer: () => response.arrayBuffer(),
            text: () => response.text(),
          };
    },
  };
}
