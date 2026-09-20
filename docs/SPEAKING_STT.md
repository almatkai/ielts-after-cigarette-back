# Speaking STT pipeline

Speaking audio is processed asynchronously:

```text
React MediaRecorder -> Go API -> MinIO
                         |
                         v
                 PostgreSQL + Redis
                         |
                         v
                    Go worker
                    /       \
       faster-whisper        Qwen
```

The Python service is deliberately stateless. It accepts one private multipart
`audio` upload at `POST /transcribe` and returns raw English transcript,
segments, word timestamps and durations. It has no PostgreSQL, Redis, MinIO or
IELTS knowledge.

The Go worker owns retries, idempotency, speech metrics and persistence. STT
and Qwen assessment are separate jobs, so an AI provider retry never repeats
Whisper. PostgreSQL is the durable source of truth; Redis only wakes workers.

## Local shared-dev start

Fill `.env.dev` from `.env.dev.example`, then run:

```powershell
docker compose --env-file .env.dev -f docker-compose.dev.yml --profile tools run --rm migrate
docker compose --env-file .env.dev -f docker-compose.dev.yml up -d --build speech-service worker backend
```

The first readiness request downloads the model into the `speech_models`
Docker volume:

```powershell
curl.exe http://localhost:8001/health/ready
```

Health checks:

```powershell
curl.exe http://localhost:8001/health/live
curl.exe http://localhost:8080/health/ready
docker compose --env-file .env.dev -f docker-compose.dev.yml logs -f worker speech-service
```

For CPU development use:

```env
SPEECH_MODEL=small.en
SPEECH_DEVICE=cpu
SPEECH_COMPUTE_TYPE=int8
```

For an NVIDIA production worker use a GPU-enabled image/profile and:

```env
SPEECH_MODEL=distil-large-v3
SPEECH_DEVICE=cuda
SPEECH_COMPUTE_TYPE=float16
```

`AI_SPEAKING_AUDIO_ENABLED` must remain `false` for the text-only Alem AI
Qwen model. Qwen receives questions, transcript and deterministic metrics. It
does not receive audio and does not score Pronunciation. Results clearly mark
Pronunciation as unavailable until an acoustic pronunciation model is added.

## Stored data

- MinIO: private original recording.
- `speaking_recordings`: object key and monotonic recording revision.
- `speaking_transcriptions`: transcript, segments, words, durations, metrics,
  processing state and retry diagnostics.
- `speaking_assessment_jobs`: independent Qwen state and retry diagnostics.
- `speaking_evaluations`: three text-supported criteria; pronunciation is
  nullable.

Both stages retry at most three times: immediately, after 10 seconds and after
60 seconds. A replacement recording increments its revision; completion from
an obsolete worker cannot overwrite the newer result. If all assessment
attempts fail, the student attempt returns to `IN_PROGRESS` so it can be
reopened and submitted again instead of remaining permanently locked.
