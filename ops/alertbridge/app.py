#!/usr/bin/env python3
"""Alertbridge: delivers IELTS alerts to one Telegram chat.

Inputs:
  1. Prometheus alert rules (polls /api/v1/alerts; sends firing/resolved).
  2. GlitchTip webhook (POST /glitchtip; new/resolved error issues).

It also self-provisions the chat: the first person who presses Start on the
bot becomes the recipient (this is a private alerting bot). The chat id is
persisted to a state file so restarts keep it.
"""
import argparse
import json
import os
import pathlib
import sys
import threading
import time
import urllib.error
import urllib.request

TELEGRAM_API = "https://api.telegram.org"
STATE_DIR = os.environ.get("STATE_DIR", "/data")
STATE_FILE = pathlib.Path(STATE_DIR) / "telegram-chat.json"
PROMETHEUS_URL = os.environ.get("PROMETHEUS_URL", "http://prometheus:9090")
LISTEN_PORT = int(os.environ.get("PORT", "8000"))
POLL_SECONDS = int(os.environ.get("POLL_SECONDS", "30"))

token = os.environ["TELEGRAM_BOT_TOKEN"].strip()
_lock = threading.Lock()
_chat_id = None
# Alerts already announced: key -> "firing"|"resolved"
_announced: dict[str, str] = {}
_last_update_id = 0


def tg(method: str, payload: dict | None = None) -> dict:
    data = json.dumps(payload).encode() if payload is not None else None
    request = urllib.request.Request(
        f"{TELEGRAM_API}/bot{token}/{method}", data=data,
        headers={"Content-Type": "application/json"}, method="POST",
    )
    with urllib.request.urlopen(request, timeout=15) as response:
        return json.load(response)


def announce(text: str) -> bool:
    global _chat_id
    with _lock:
        chat_id = _chat_id
    if chat_id is None:
        print("no chat configured yet; dropping:", text.splitlines()[0], flush=True)
        return False
    try:
        tg("sendMessage", {"chat_id": chat_id, "text": text, "disable_web_page_preview": True})
        print("sent to chat", chat_id, ":", text.splitlines()[0], flush=True)
        return True
    except Exception as error:  # never crash the pollers on Telegram hiccups
        print("telegram send failed:", error, flush=True)
        return False


def load_state() -> None:
    global _chat_id, _last_update_id
    if STATE_FILE.exists():
        state = json.loads(STATE_FILE.read_text())
        _chat_id = state.get("chat_id")
        _last_update_id = state.get("last_update_id", 0)
        if _chat_id:
            print("restored chat_id", _chat_id, flush=True)


def save_state() -> None:
    STATE_FILE.parent.mkdir(parents=True, exist_ok=True)
    tmp = STATE_FILE.with_suffix(".tmp")
    tmp.write_text(json.dumps({"chat_id": _chat_id, "last_update_id": _last_update_id}))
    tmp.replace(STATE_FILE)


def discover_chat() -> None:
    """Accept the first private-chat user as the alert recipient."""
    global _chat_id, _last_update_id
    try:
        updates = tg("getUpdates", {"offset": _last_update_id + 1, "timeout": 0})
    except Exception as error:
        print("getUpdates failed:", error, flush=True)
        return
    for update in updates.get("result", []):
        _last_update_id = max(_last_update_id, update["update_id"])
        message = update.get("message") or update.get("my_chat_member", {})
        chat = message.get("chat", {})
        if chat.get("type") == "private":
            with _lock:
                if _chat_id is None:
                    _chat_id = chat["id"]
                    save_state()
                    print("chat_id discovered:", _chat_id, flush=True)
                    announce(
                        "✅ IELTS alerting connected.\n"
                        "Сюда будут приходить алерты мониторинга (Prometheus) "
                        "и новые ошибки из GlitchTip. Это тестовое сообщение."
                    )


def poll_prometheus() -> None:
    try:
        with urllib.request.urlopen(f"{PROMETHEUS_URL}/api/v1/alerts", timeout=10) as response:
            payload = json.load(response)
    except Exception as error:
        print("prometheus poll failed:", error, flush=True)
        return
    current: dict[str, str] = {}
    for alert in payload.get("data", {}).get("alerts", []):
        # Pending alerts have not yet satisfied the rule's `for:` duration.
        if alert["state"] != "firing":
            continue
        current[alert["labels"]["alertname"]] = "firing"
    with _lock:
        announced = dict(_announced)
    for name, state in current.items():
        if announced.get(name) != "firing":
            with _lock:
                _announced[name] = "firing"
            announce(f"🔴 FIRING: {name}\n{describe(payload, name)}")
    for name, state in announced.items():
        if state == "firing" and name not in current:
            with _lock:
                _announced[name] = "resolved"
            announce(f"🟢 RESOLVED: {name}")


def describe(payload: dict, name: str) -> str:
    for alert in payload.get("data", {}).get("alerts", []):
        if alert["labels"].get("alertname") == name and alert["state"] == "firing":
            annotations = alert.get("annotations", {})
            return annotations.get("summary", "")
    return ""


def poll_forever(target, seconds: int, name: str) -> None:
    while True:
        try:
            target()
        except Exception as error:
            print(f"{name} poller crashed, retrying:", error, flush=True)
        time.sleep(seconds)


def run_webhook_server() -> None:
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            if self.path != "/glitchtip":
                self.send_response(404)
                self.end_headers()
                return
            length = int(self.headers.get("Content-Length", "0"))
            if length > 1 << 20:
                self.send_response(413)
                self.end_headers()
                return
            body = self.rfile.read(length)
            self.send_response(204)
            self.end_headers()
            try:
                handle_glitchtip(json.loads(body))
            except Exception as error:
                print("glitchtip webhook failed:", error, flush=True)

        def log_message(self, *args):
            pass

    server = ThreadingHTTPServer(("0.0.0.0", LISTEN_PORT), Handler)
    server.serve_forever()


def handle_glitchtip(payload: dict) -> None:
    """GlitchTip 'general webhook' posts a Slack-compatible payload."""
    for attachment in payload.get("attachments", []):
        title = attachment.get("title", "error")
        link = attachment.get("title_link", "")
        culprit = attachment.get("text", "")
        project = next(
            (
                field["value"]
                for field in attachment.get("fields", [])
                if field.get("title") == "Project"
            ),
            "?",
        )
        announce(
            f"🐞 GlitchTip: новая ошибка [{project}]\n"
            f"{title}\n"
            f"{culprit}\n{link}"
        )


def send_test() -> int:
    load_state()
    if _chat_id is None:
        print("chat not configured yet")
        return 1
    return 0 if announce("✅ Тестовая доставка IELTS алертов") else 1


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--send-test", action="store_true")
    args = parser.parse_args()
    load_state()
    if args.send_test:
        return send_test()
    threading.Thread(target=poll_forever, args=(discover_chat, POLL_SECONDS, "chat"), daemon=True).start()
    threading.Thread(target=poll_forever, args=(poll_prometheus, POLL_SECONDS, "prometheus"), daemon=True).start()
    print("alertbridge started", flush=True)
    run_webhook_server()
    return 0


if __name__ == "__main__":
    sys.exit(main())
