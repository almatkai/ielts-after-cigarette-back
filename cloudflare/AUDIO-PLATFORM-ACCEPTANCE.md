# Native Cloudflare audio acceptance

On 2026-10-28, the deployed `ielts-audio-dev` version
`1498a6aa-b13c-4f6b-8387-826828d53011` converted both inputs below on actual
Cloudflare. The audio runtime and its configuration were unchanged. The same
`ielts-audio-dev-v1` Durable Object identity used by the application handled the
calls. No speech, transcription or AI provider was called.

| Input                                                               | Input bytes | WAV bytes | PCM duration | Audio DO CPU | Outcome |
| ------------------------------------------------------------------- | ----------: | --------: | -----------: | -----------: | ------- |
| Verified educational Listening MP3 from the imported DEV pack       |       3,464 |    32,078 |          1 s |        40 ms | ok      |
| Synthetic browser-style 48 kHz mono Opus/WebM, tone plus pink noise |   1,411,737 | 5,760,078 |        180 s |     1,149 ms | ok      |

The local verifier consumed every output byte, matched its SHA-256 against the
private blob store's checksum, checked the WAV header and exact byte count, and
confirmed PCM16 mono at 16 kHz and expected duration. The educational input was
silence; the synthetic input had nonzero samples. Wasm linear memory was
33,554,432 bytes for both conversions. Metadata-only native tails reported no
exceptions or memory-limit outcome. The original object's version, size, checksum
and content type were unchanged.

This establishes that these cases passed actual platform enforcement. It does
not measure total isolate peak memory or establish the worst case at the existing
12 MiB compressed-input ceiling, all codecs or arbitrarily long audio. The private
helper's 180-second streaming request reported 14 ms CPU and `outcome: ok`; this
is not evidence of a strict sub-10 ms ingress bound. Full SHA and WAV verification
ran in the local CLI, while the deployed audio/blob Durable Objects performed
conversion, storage and native checksum generation.

All probe-derived output and both uniquely named synthetic fixture uploads were
removed. The final verifier checked derived/fixture heads were absent. The
original import remained active and matched its original receipt. The temporary
`/probe/audio` and `/probe/audio-state` helper routes and extra audio binding were
removed; the original private migration helper was redeployed as version
`4e041de7-58db-45a2-8cd1-cddc682fa795`. Both removed routes return 404. The root's
local Wrangler bridge was left running for cutover work. Native tail sessions
opened by this probe were stopped.

Machine-readable evidence, runtime/Wasm hashes and measurement boundaries:
[schema/audio-platform-acceptance.json](schema/audio-platform-acceptance.json).
No private object keys, user records or credentials are included.

## Reproduction tooling

`tools/audio-probe-worker.mjs` contains **inactive private-helper-only** handlers.
They are intentionally not imported by the deployed migration helper. For a
future authorized acceptance run, temporarily import `probeAudio` and
`probeAudioState` into `tools/migration-worker.mjs`, route the two POST paths above
to them inside its private named entrypoint, and add an `IAC_AUDIO` binding to
`IACAudio` in `ielts-audio-dev`. Deploy only this temporary private helper;
its default public HTTP handler must continue returning 404.

Start the existing local OAuth service-binding bridge as described in
[tools/migration-local/README.md](tools/migration-local/README.md). Prepare a mode
600 input descriptor containing the private key, exact size, SHA-256 and expected
PCM duration from an approved source export. The CLI does not print the key:

```sh
node tools/audio-platform-probe.mjs --execute --input .sensitive/audio-probe-input.json --endpoint http://127.0.0.1:8794 --report .artifacts/audio-native-acceptance.json
```

For synthetic input, generate a fixture locally, upload under a fresh unique
`_migration-probe/audio-<uuid>.webm` key through the private helper, and set
`temporaryFixture: true` and `requireNonzero: true` in the descriptor. The handler
checks that prefix before allowing fixture cleanup. The source bytes can be
reproduced with local FFmpeg:

```sh
ffmpeg -v error -y -f lavfi -i 'sine=frequency=440:sample_rate=48000:duration=180' -f lavfi -i 'anoisesrc=color=pink:sample_rate=48000:duration=180:amplitude=0.08' -filter_complex '[0:a][1:a]amix=inputs=2:normalize=0' -ac 1 -c:a libopus -b:a 64k .artifacts/audio-native-browser180.webm
```

The output stream removes only its own generated temporary WAV on EOF, error or
cancellation. Explicit synthetic fixtures are removed too; real originals are
preserved. Unit tests cover cleanup, cancellation races, malformed input and WAV
format validation. After the run, remove only the temporary helper imports,
routes and binding, verify its diff is empty, redeploy the original helper, and
confirm both probe routes return 404 and the original import receipt is unchanged.
