#!/usr/bin/env node
// Read-only aggregate audit. Reuses the existing Wrangler OAuth login; no billing
// endpoints, namespace access, mutations, new credentials or request-level data.
import { readFile, writeFile } from "node:fs/promises";
import { homedir } from "node:os";
import { join } from "node:path";
import { createHash } from "node:crypto";
const config = await readFile(
  join(homedir(), "Library/Preferences/.wrangler/config/default.toml"),
  "utf8",
);
const token = config.match(/^oauth_token\s*=\s*"([^"]+)"/m)?.[1];
if (!token)
  throw Error("Existing Wrangler OAuth is unavailable; no token is printed.");
const end = new Date().toISOString(),
  start = end.slice(0, 10) + "T00:00:00Z",
  filter = `filter:{datetime_geq:"${start}",datetime_leq:"${end}"}`,
  interval = "{estimate isValid lower upper sampleSize}",
  query = `{viewer{accounts(filter:{accountTag:"ab39c7a0ec52f861379630cccb01061f"}){
    periodic:durableObjectsPeriodicGroups(${filter},limit:2){count sum{rowsRead rowsWritten duration activeTime cpuTime exceededCpuErrors exceededMemoryErrors fatalInternalErrors} confidence(level:0.95){sum{rowsRead${interval} rowsWritten${interval} duration${interval}}}}
    invocations:durableObjectsInvocationsAdaptiveGroups(${filter},limit:2){sum{requests errors} confidence(level:0.95){sum{requests${interval}}}}
    latest:durableObjectsPeriodicGroups(${filter},limit:1,orderBy:[datetimeMinute_DESC]){dimensions{datetimeMinute}}
  }}}`;
const response = await fetch("https://api.cloudflare.com/client/v4/graphql", {
  method: "POST",
  headers: {
    authorization: "Bearer " + token,
    "content-type": "application/json",
  },
  body: JSON.stringify({ query }),
  redirect: "error",
  signal: AbortSignal.timeout(30000),
});
const data = await response.json();
if (!response.ok || data.errors?.length) {
  console.error(
    JSON.stringify({
      httpStatus: response.status,
      errors: (data.errors ?? []).map((e) => ({
        message: e.message,
        code: e.extensions?.code,
      })),
    }),
  );
  process.exitCode = 1;
} else {
  const account = data.data?.viewer?.accounts?.[0],
    periodic = account?.periodic?.[0],
    invocations = account?.invocations?.[0];
  if (!periodic || !invocations)
    throw Error("No account aggregate was returned.");
  const report = {
    checkedAt: end,
    windowStartUTC: start,
    latestReportedMinute: account.latest?.[0]?.dimensions.datetimeMinute,
    querySha256: createHash("sha256").update(query).digest("hex"),
    accountDOUsage: periodic.sum,
    requestsEstimate: invocations.sum.requests,
    requestErrorsEstimate: invocations.sum.errors,
    confidence95: {
      periodic: periodic.confidence.sum,
      requests: invocations.confidence.sum.requests,
    },
    visibleRowsWrittenHeadroom: 100000 - periodic.sum.rowsWritten,
    warnings: [
      "Analytics reporting lag applies; headroom is not reserved.",
      "Workers Free/Paid subscription and total stored bytes are not verified by this query.",
    ],
  };
  const output = process.argv.indexOf("--output");
  if (output >= 0)
    await writeFile(
      process.argv[output + 1],
      JSON.stringify(report, null, 2) + "\n",
      { flag: "wx" },
    );
  console.log(JSON.stringify(report));
}
