# Listening STT audit and repair plan — 2026-10-03

Requested behavior: transcribe the actual Listening audio and use that evidence
to help students **only after submission**. Administrators may inspect generated
transcripts and explanations before publishing.

This audit inspected the live production container/database and both repositories.
It exercised the existing Go STT client with a real audio sample, plus isolated
diagnostic probes. Production tests, credentials, and deployment were not changed.
The STT settings supplied in the conversation are available for implementation;
secret values are intentionally excluded from this document.

## Follow-up

The production configuration findings below describe the initial audit. The
subsequent [production synchronization](LISTENING_PRODUCTION_SYNC_2026-10-03.md)
wires STT/AI settings and installs ffmpeg. The data/access and processing findings
remain open.

## Current flow

1. An administrator attaches audio and saves a Listening test.
2. The editor calls `POST /api/v1/admin/listening/tests/{testID}/transcribe`.
3. `listening.Service.TranscribeTest` reads the saved draft and opens its original
   audio through the configured object store. Production now reads local files.
4. `STTService.Transcribe` sends multipart `file`, model `speech-to-text`, language
   `en`, and `response_format=verbose_json` to the STT endpoint. It returns real
   transcript text and timed segments. Compressed WebM/OGG/M4A require ffmpeg.
5. A separate chat-model call receives transcript segments, questions, and answer
   keys. It proposes answer timestamps, evidence quotes, and Russian hints.
6. Transcript/segments are saved on parts; alignment fields are saved in question
   content. Saving creates a new draft version. Publication is a separate action;
   existing attempts remain pinned to their original published version.
7. Submitted-attempt reviews and the mistake retry dialog consume these fields
   to show evidence, hints, and audio replay. The normal student runner does not
   have a transcript-help UI. Administrator preview can show transcript evidence.

STT and alignment are distinct operations. A valid STT credential does not establish
that the separately configured chat endpoint accepts that credential.

## Verified findings

| Finding | Evidence and impact |
| --- | --- |
| Production STT is unauthenticated | Running container has no `STT_API_KEY`, `AI_API_KEY`, or `OPENROUTER_API_KEY`. Its STT URL falls back to the correct Alem endpoint, but requests have no authorization header. |
| Deployment omits the settings | Production Compose does not pass `STT_API_KEY`/`STT_API_URL`. The deploy workflow does not populate STT or AI configuration in `production.env`. Adding a key to GitHub alone would not fix the running container. |
| Provider and basic response contract work | Existing Go client + a real 20-second MP3 clip failed with production-equivalent settings: `401 Authentication Error, No api key passed in.` The same clip with configured local credentials produced 195 characters, five segments, and duration 20 seconds. This confirms the short MP3 path, not full-length throughput or accuracy. |
| Alignment can fail silently | `TranscribeTest` applies alignment only when its error is nil, otherwise proceeds to save and return success. An isolated probe with a successful STT response and alignment HTTP 401 reproduced this. The editor always says timestamps and hints were generated. |
| Generated evidence is not validated | Isolated probes demonstrated acceptance of empty STT text and of alignment containing negative/out-of-range timestamps plus a quote absent from the transcript. `Align` also does not explicitly check the upstream HTTP status before decoding. |
| Production cannot transcode supported compressed formats | Runtime image and running container have no ffmpeg. WebM/OGG/M4A fail at conversion; MP3 follows a different path and does not require this conversion. |
| Transcripts are exposed before submission | A read-only student request for the published test returned four transcripts totaling 66,796 characters and 1,268 segments without any submission requirement. `publicTest` copies transcript/segments into student material and retains answer timestamps in public question content. Hiding the UI does not enforce the requested policy. |
| Transcription ignores unsaved editor changes | The mutation only sends the test ID, so it reads the stored draft. Its response replaces the form. Newly assigned audio or question edits can be ignored and unsaved changes overwritten. |
| Processing is synchronous and only cached within one request | STT and alignment run inside an HTTP request with a five-minute media deadline. There is no persistent Listening job, retry state, or transcript cache. Long-file/proxy failures were not measured in this audit. |

Production inventory:

- 20 tests, 80 current parts, 800 questions.
- Five tests have transcripts: the four Cambridge 21 tests and Greek Island Holidays.
- The remaining 15 tests / 60 parts have no transcripts.
- 200 questions have both answer timestamps and quotes. Nine of these quotes did
  not match transcript text after case and whitespace normalization; inspect
  these rather than assuming fabrication, since punctuation can affect matching.
- All 20 tests share one audio asset across their four parts. Existing processing
  sends that asset once per request but copies its complete transcript into every
  part, then performs alignment separately for each part's questions.
- One test is published; the other 19 are drafts. STT does not make drafts visible
  to students automatically.

The repository also contains a local faster-whisper service and asynchronous
Speaking worker. Listening does not use that pipeline. It is a potential reuse
point for job/state handling, not a currently deployed Listening solution.

## Implementation order

### 1. Enforce post-submission access

- Remove transcript, segments, answer evidence, and answer-specific timestamps
  from student library, in-progress attempt, and full-mock material responses.
- Preserve administrator preview through an explicit administrator projection.
- Expose transcript-based help through a submitted-attempt endpoint that checks
  attempt ownership and submitted status and reads its pinned material version.
- Keep the submitted review/mistake UI working through that authorized response.

Acceptance: in-progress students receive no transcript-based help data; another
user cannot fetch an attempt's help; the owner receives evidence after submission;
admin preview remains complete. Tests cover library, attempt, and full-mock routes.

### 2. Repair production configuration and supported formats

- Wire `STT_API_KEY` as a secret and `STT_API_URL` through the GitHub production
  workflow, generated env file, and Compose environment.
- Wire and independently verify `AI_API_KEY`, `AI_CHAT_COMPLETIONS_URL`, and
  `AI_MODEL` for alignment. Do not assume the STT key works for the chat endpoint.
- Install ffmpeg in the runtime image or normalize compressed audio in a worker
  image that includes it. Retain local production media volumes.
- Add an explicit STT capability/configuration check and actionable dependency
  errors; never log secret values.

Acceptance: the actual production container transcribes a known real sample with
nonempty text and valid segments; compressed-format conversion is checked; a
subsequent deployment retains configuration. Verify one complete IELTS audio file.

### 3. Make processing durable and report partial results accurately

- Save dirty editor changes before scheduling transcription; pin the saved test
  revision and source audio hash. Prevent obsolete completion from overwriting
  a newer draft or replacement audio.
- Process transcription as a background job with progress, bounded retries, and
  persistent results per unique audio asset/hash and provider/model configuration.
- Save successful STT independently of alignment, so a chat retry does not repeat
  transcription. Use explicit states such as queued, transcribing, aligning,
  ready, partial, and failed, with safe error details.
- Reject empty transcripts; validate segment ranges and required timestamps.
- Have the editor display actual stage results instead of unconditional success
  or the hardcoded 10–25-second promise.

Acceptance: alignment 401 produces a visible partial result with a retained
transcript; retrying alignment does not resend audio; duplicate job requests are
idempotent; shared four-part audio is transcribed once; stale jobs cannot overwrite
newer content. A failed run does not erase a previously valid transcript.

### 4. Ground explanations in the actual transcript

- Let AI select transcript segment identifiers and draft an explanation/hint;
  derive timestamps and quotes from those stored segments rather than trusting
  arbitrary generated values.
- Validate question identity, timestamp order/bounds, quote correspondence, and
  coverage. Leave unresolved questions explicitly unaligned for administrator
  review rather than manufacturing evidence.
- Invalidate stale generated evidence when its audio or transcript changes.
- Retain source hash, provider/model, generation time, and review status.

Acceptance: the diagnostic cases for fabricated quotes and invalid ranges are
rejected. Displayed explanation quotes correspond to the audio-backed transcript.
Human review checks a representative set of answer clips and STT recognition
errors; raw STT text is not treated as infallible.

### 5. Backfill and deliver the post-submission help

- Generate real transcripts for the 15 missing tests with resumable jobs, then
  align questions and review warnings plus the nine existing quote mismatches.
- Show full transcript, highlighted evidence, a short explanation, and an audio
  clip around the answer only in submitted review/mistake practice. Replay should
  stop at the selected clip end; the current player seeks to a start but does not
  enforce the displayed end time.
- Review and publish generated drafts separately. Existing submitted attempts
  stay attached to their original versions; any historical backfill should use
  evidence keyed to the original audio/version rather than changing answer keys
  or grades.

Acceptance: an owner completes a Listening attempt, opens a wrong answer, sees
real transcript evidence, and plays the matching bounded audio clip. No transcript
help is available in the same attempt before submission. New generated data
survives deployment and is available on approved published versions.

## Limits of this audit

No full-length production transcription or bulk backfill was run. The live STT
probe used a 20-second clip. Existing transcript provenance and all 200 timestamps
were not checked against the complete recordings. The audio-provider request
succeeded, but AI alignment credentials/provider availability still need independent
verification during implementation. No product fix or production setting was
changed as part of this planning task.
