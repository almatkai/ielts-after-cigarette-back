#!/usr/bin/env python3
"""Resumable, draft-only importer for IELTS Reading, Listening, and Writing bundles.

Manifest format is documented in scripts/cambridge16-import-manifest.example.json.
Uses only the Python standard library. It never calls publish/archive endpoints.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import sys
import time
import uuid
from pathlib import Path
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

MEDIA_RE = re.compile(r"\{\{MEDIA:([A-Za-z0-9_.-]+)\}\}")
API_ROOT = "/api/v1/admin"


class ImportFailure(RuntimeError):
    pass


class Importer:
    def __init__(self, base_url: str, token_file: Path | None, token_env: str,
                 timeout: float, wait_auth: float, dry_run: bool):
        self.base_url = base_url.rstrip("/")
        self.token_file = token_file
        self.token_env = token_env
        self.timeout = timeout
        self.wait_auth = wait_auth
        self.dry_run = dry_run
        self._auth_hash = self._token_hash()

    def _token(self) -> str:
        token = os.environ.get(self.token_env, "").strip()
        if not token and self.token_file and self.token_file.exists():
            token = self.token_file.read_text(encoding="utf-8").strip()
        token = re.sub(r"^Bearer\s+", "", token, flags=re.I).strip()
        if not token:
            raise ImportFailure(
                f"No admin token. Set {self.token_env} or write one to {self.token_file}"
            )
        return token

    def _token_hash(self) -> str:
        try:
            return hashlib.sha256(self._token().encode()).hexdigest()
        except ImportFailure:
            return ""

    def _wait_for_rotated_token(self, old_hash: str) -> None:
        if self.wait_auth <= 0 or not self.token_file:
            raise ImportFailure("Admin API returned 401; token may be expired. Rotate the token and rerun.")
        deadline = time.monotonic() + self.wait_auth
        print(f"401: waiting up to {self.wait_auth:g}s for a new token in {self.token_file}", file=sys.stderr)
        while time.monotonic() < deadline:
            time.sleep(2)
            new_hash = self._token_hash()
            if new_hash and new_hash != old_hash:
                self._auth_hash = new_hash
                print("Detected a rotated token; retrying", file=sys.stderr)
                return
        raise ImportFailure("Timed out waiting for a rotated admin token")

    def request(self, method: str, path: str, payload: Any = None,
                *, multipart: tuple[str, Path, str] | None = None,
                form_fields: dict[str, str] | None = None) -> Any:
        body: bytes
        content_type: str
        if multipart:
            field, file_path, mime = multipart
            boundary = "----IACImport" + uuid.uuid4().hex
            data = file_path.read_bytes()
            filename = file_path.name.replace('"', "")
            chunks = [
                f"--{boundary}\r\nContent-Disposition: form-data; name=\"{field}\"; filename=\"{filename}\"\r\nContent-Type: {mime}\r\n\r\n".encode(),
                data,
                b"\r\n",
            ]
            for name, value in (form_fields or {}).items():
                chunks.append(f"--{boundary}\r\nContent-Disposition: form-data; name=\"{name}\"\r\n\r\n{value}\r\n".encode())
            chunks.append(f"--{boundary}--\r\n".encode())
            body = b"".join(chunks)
            content_type = f"multipart/form-data; boundary={boundary}"
        else:
            body = None if payload is None else json.dumps(payload, ensure_ascii=False).encode("utf-8")
            content_type = "application/json"

        old_hash = self._token_hash()
        while True:
            headers = {"Authorization": "Bearer " + self._token(), "Accept": "application/json"}
            if body is not None:
                headers["Content-Type"] = content_type
            request = Request(self.base_url + path, data=body, headers=headers, method=method)
            try:
                with urlopen(request, timeout=self.timeout) as response:
                    raw = response.read()
                    return json.loads(raw) if raw else {}
            except HTTPError as exc:
                detail = exc.read().decode("utf-8", "replace")
                if exc.code == 401:
                    self._wait_for_rotated_token(old_hash)
                    old_hash = self._auth_hash
                    continue
                if exc.code >= 500:
                    raise ImportFailure(f"{method} {path}: HTTP {exc.code}: {detail[:1200]}") from exc
                raise ImportFailure(f"{method} {path}: HTTP {exc.code}: {detail[:3000]}") from exc
            except URLError as exc:
                raise ImportFailure(f"Cannot reach {self.base_url}: {exc}") from exc

    def upload(self, kind: str, file_path: Path) -> str:
        if self.dry_run:
            print(f"  [dry-run] upload {kind}: {file_path}")
            return "00000000-0000-0000-0000-000000000000"
        endpoint = "/writing/media" if kind == "writing_image" else "/listening/media"
        if kind == "writing_image":
            result = self.request("POST", API_ROOT + endpoint, multipart=("file", file_path, mime_type(file_path)))
        else:
            result = self.request("POST", API_ROOT + endpoint,
                                  multipart=("file", file_path, mime_type(file_path)),
                                  form_fields={"kind": "audio" if kind == "listening_audio" else "image"})
        media_id = result.get("id")
        if not media_id:
            raise ImportFailure(f"Upload did not return an id: {file_path}")
        print(f"  uploaded {file_path.name} -> {media_id}")
        return media_id

    def exists(self, kind: str, title: str) -> dict[str, Any] | None:
        endpoint = {
            "reading": "/reading/materials",
            "listening": "/listening/tests",
            "writing": "/writing/materials",
        }[kind]
        result = self.request("GET", API_ROOT + endpoint)
        for item in result.get("items", []):
            if item.get("title", "").strip().casefold() == title.strip().casefold():
                return item
        return None

    def import_item(self, item: dict[str, Any], manifest_dir: Path) -> dict[str, Any] | None:
        kind = item["kind"]
        title = item["title"]
        existing = self.exists(kind, title)
        if existing:
            if existing.get("status") != "DRAFT":
                raise ImportFailure(f"'{title}' already exists with status {existing.get('status')}; refusing to change it")
            print(f"SKIP existing DRAFT: {title} ({existing.get('id')})")
            return existing

        source_path = resolve_path(manifest_dir, item["source"])
        raw = source_path.read_text(encoding="utf-8")
        media_ids: dict[str, str] = {}
        for key, media in item.get("media", {}).items():
            path = resolve_path(manifest_dir, media["path"])
            media_ids[key] = self.upload(media["kind"], path)

        def substitute(text: str) -> str:
            return MEDIA_RE.sub(lambda m: media_ids.get(m.group(1), m.group(0)), text)

        if kind == "reading":
            source = substitute(raw)
            parsed = self.request("POST", API_ROOT + "/reading/import/parse", {
                "source": source,
                "examType": item.get("examType", "academic"),
                "difficulty": item.get("difficulty", "intermediate"),
            })
            require_no_errors(parsed, title)
            passages = []
            prefix = item.get("slugPrefix", slugify(title))
            for index, passage in enumerate(parsed.get("passages", []), start=1):
                material = passage["material"]
                material["slug"] = f"{prefix}-passage-{index}"
                material["sourceTitle"] = item.get("sourceTitle", "")
                material["sourceUrl"] = item.get("sourceUrl", "")
                passages.append(material)
            payload = {"title": title, "durationMinutes": item.get("durationMinutes", 60), "passages": passages}
            if self.dry_run:
                print(f"  [dry-run] validated {len(passages)} passages")
                return None
            result = self.request("POST", API_ROOT + "/reading/import", payload)
            imported = result.get("items", [])
            if len(imported) != 1:
                raise ImportFailure(f"Reading import returned {len(imported)} items, expected 1")
            created = imported[0]
        elif kind == "listening":
            source = substitute(raw)
            parsed = self.request("POST", API_ROOT + "/listening/import/parse", {
                "source": source, "examType": item.get("examType", "academic")
            })
            require_no_errors(parsed, title)
            payload = parsed["test"]
            payload["title"] = title
            payload["slug"] = item.get("slug", slugify(title))
            # Do not add sourceTitle/sourceUrl: the Listening SaveInput rejects unknown fields.
            if self.dry_run:
                print(f"  [dry-run] validated {len(payload.get('parts', []))} parts")
                return None
            created = self.request("POST", API_ROOT + "/listening/import", payload)
        else:
            source = substitute(raw)
            parsed = self.request("POST", API_ROOT + "/writing/import/parse", {"source": source})
            require_no_errors(parsed, title)
            materials = parsed.get("materials", [])
            matches = [material for material in materials if material.get("title", "").strip().casefold() == title.strip().casefold()]
            if len(matches) == 1:
                materials = matches
            elif len(materials) != 1:
                raise ImportFailure(f"Writing source returned {len(materials)} materials; add an exact title for selection")
            if self.dry_run:
                print("  [dry-run] validated 1 writing material")
                return None
            result = self.request("POST", API_ROOT + "/writing/import", {"materials": materials})
            created_items = result.get("items", [])
            if len(created_items) != 1:
                raise ImportFailure(f"Writing import returned {len(created_items)} items, expected 1")
            created = created_items[0]

        status = created.get("status")
        if status != "DRAFT":
            raise ImportFailure(f"'{title}' was created with unexpected status {status!r}; check backend immediately")
        print(f"IMPORTED DRAFT: {title} ({created.get('id')})")
        return created


def resolve_path(root: Path, value: str) -> Path:
    path = Path(value).expanduser()
    return path if path.is_absolute() else (root / path).resolve()


def mime_type(path: Path) -> str:
    suffix = path.suffix.lower()
    return {".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".wav": "audio/wav",
            ".ogg": "audio/ogg", ".webm": "audio/webm", ".png": "image/png",
            ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp"}.get(suffix, "application/octet-stream")


def slugify(value: str) -> str:
    value = value.lower().replace("—", "-").replace("–", "-")
    value = re.sub(r"[^a-z0-9]+", "-", value).strip("-")
    return value[:150] or "ielts-import"


def require_no_errors(parsed: dict[str, Any], title: str) -> None:
    errors = parsed.get("errors", [])
    if errors:
        raise ImportFailure(f"Parse failed for '{title}':\n" + json.dumps(errors, ensure_ascii=False, indent=2))


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("manifest", type=Path, help="Bundle manifest JSON")
    parser.add_argument("--base-url", help="Backend base URL (default manifest baseUrl or http://localhost:8090)")
    parser.add_argument("--token-file", type=Path, default=Path("/tmp/iac-admin-token"),
                        help="File containing a short-lived admin token (default: /tmp/iac-admin-token)")
    parser.add_argument("--token-env", default="IAC_ADMIN_TOKEN", help="Environment variable containing token")
    parser.add_argument("--timeout", type=float, default=90)
    parser.add_argument("--wait-auth", type=float, default=0,
                        help="On 401, wait this many seconds for token-file rotation and retry")
    parser.add_argument("--dry-run", action="store_true", help="Parse/validate; do not upload or import")
    parser.add_argument("--only", action="append", default=[], help="Import only matching title substring (repeatable)")
    args = parser.parse_args()

    manifest_path = args.manifest.resolve()
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    items = manifest.get("items", [])
    if not isinstance(items, list) or not items:
        raise ImportFailure("Manifest must contain a non-empty items array")
    selected = [item for item in items if not args.only or any(x.casefold() in item.get("title", "").casefold() for x in args.only)]
    if not selected:
        raise ImportFailure("No manifest items matched --only")
    base_url = args.base_url or manifest.get("baseUrl", "http://localhost:8090")
    importer = Importer(base_url, args.token_file, args.token_env, args.timeout, args.wait_auth, args.dry_run)
    errors = 0
    for index, item in enumerate(selected, start=1):
        title = item.get("title", "<untitled>")
        print(f"[{index}/{len(selected)}] {item.get('kind')} — {title}")
        try:
            importer.import_item(item, manifest_path.parent)
        except (ImportFailure, KeyError, ValueError, OSError) as exc:
            errors += 1
            print(f"ERROR: {exc}", file=sys.stderr)
            if not manifest.get("continueOnError", False):
                break
    print(f"Finished: {len(selected) - errors}/{len(selected)} succeeded or skipped; {errors} error(s)")
    return 1 if errors else 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (ImportFailure, json.JSONDecodeError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        raise SystemExit(1)
