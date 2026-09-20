import os
import tempfile
import time
from pathlib import Path
from threading import Lock

from fastapi import FastAPI, File, Header, HTTPException, UploadFile
from faster_whisper import WhisperModel


app = FastAPI(title="IELTS Speech Service", version="1.0.0")
_model: WhisperModel | None = None
_model_lock = Lock()


def get_model() -> WhisperModel:
    global _model
    if _model is None:
        with _model_lock:
            if _model is None:
                _model = WhisperModel(
                    os.getenv("SPEECH_MODEL", "small.en"),
                    device=os.getenv("SPEECH_DEVICE", "cpu"),
                    compute_type=os.getenv("SPEECH_COMPUTE_TYPE", "int8"),
                    download_root=os.getenv("SPEECH_MODEL_CACHE", "/models"),
                )
    return _model


def authorize(value: str | None) -> None:
    expected = os.getenv("SPEECH_SERVICE_TOKEN", "").strip()
    if expected and value != f"Bearer {expected}":
        raise HTTPException(status_code=401, detail="invalid service token")


@app.get("/health/live")
def live() -> dict[str, str]:
    return {"status": "ok"}


@app.get("/health/ready")
def ready() -> dict[str, str]:
    get_model()
    return {"status": "ok"}


@app.post("/transcribe")
async def transcribe(
    audio: UploadFile = File(...),
    authorization: str | None = Header(default=None),
) -> dict:
    authorize(authorization)
    max_bytes = int(os.getenv("SPEECH_MAX_AUDIO_BYTES", str(12 * 1024 * 1024)))
    suffix = Path(audio.filename or "recording.webm").suffix[:12] or ".webm"
    started = time.perf_counter()
    path = ""
    try:
        with tempfile.NamedTemporaryFile(suffix=suffix, delete=False) as target:
            path = target.name
            total = 0
            while chunk := await audio.read(1024 * 1024):
                total += len(chunk)
                if total > max_bytes:
                    raise HTTPException(status_code=413, detail="audio is too large")
                target.write(chunk)
        if total == 0:
            raise HTTPException(status_code=422, detail="audio is empty")

        segments_iter, info = get_model().transcribe(
            path,
            language="en",
            word_timestamps=True,
            vad_filter=True,
            vad_parameters={"min_silence_duration_ms": 500},
            condition_on_previous_text=False,
        )
        raw_segments = list(segments_iter)
        segments = []
        words = []
        for segment in raw_segments:
            segments.append({
                "start": segment.start,
                "end": segment.end,
                "text": segment.text.strip(),
            })
            for word in segment.words or []:
                words.append({
                    "word": word.word.strip(),
                    "start": word.start,
                    "end": word.end,
                    "probability": word.probability,
                })
        return {
            "text": " ".join(item["text"] for item in segments).strip(),
            "language": info.language,
            "languageProbability": info.language_probability,
            "durationSeconds": info.duration,
            "durationAfterVadSeconds": info.duration_after_vad,
            "processingTimeMs": round((time.perf_counter() - started) * 1000),
            "model": os.getenv("SPEECH_MODEL", "small.en"),
            "segments": segments,
            "words": words,
        }
    finally:
        await audio.close()
        if path:
            Path(path).unlink(missing_ok=True)
