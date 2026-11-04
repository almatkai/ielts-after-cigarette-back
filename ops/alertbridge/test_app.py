import importlib.util
import io
import json
import os
import pathlib
import unittest
from unittest.mock import patch

os.environ.setdefault("TELEGRAM_BOT_TOKEN", "local-test-only")
spec = importlib.util.spec_from_file_location("alertbridge", pathlib.Path(__file__).with_name("app.py"))
app = importlib.util.module_from_spec(spec)
spec.loader.exec_module(app)


def alert(route, state="firing"):
    return {"state": state, "labels": {"alertname": "APISlow", "route": route},
            "annotations": {"summary": "Slow " + route}}


class DeliveryTests(unittest.TestCase):
    def setUp(self):
        app._announced.clear()

    def poll(self, alerts, success=True):
        payload = io.StringIO(json.dumps({"status": "success", "data": {"alerts": alerts}}))
        with patch.object(app.urllib.request, "urlopen", return_value=payload), \
                patch.object(app, "announce", return_value=success) as send:
            app.poll_prometheus()
        return [call.args[0] for call in send.call_args_list]

    def test_each_route_fires_and_resolves_independently(self):
        first, second = alert("/first"), alert("/study-plan")
        self.assertEqual(len(self.poll([first])), 1)
        messages = self.poll([first, second])
        self.assertEqual(len(messages), 1)
        self.assertIn("/study-plan", messages[0])
        messages = self.poll([second])
        self.assertEqual(len(messages), 1)
        self.assertIn("RESOLVED", messages[0])
        self.assertIn("/first", messages[0])
        self.assertEqual(self.poll([second]), [])

    def test_failed_firing_delivery_retries(self):
        self.poll([alert("/study-plan")], success=False)
        self.assertEqual(len(self.poll([alert("/study-plan")])), 1)
        self.assertEqual(self.poll([alert("/study-plan")]), [])

    def test_failed_resolution_retries(self):
        self.poll([alert("/study-plan")])
        self.poll([], success=False)
        self.assertEqual(len(self.poll([])), 1)
        self.assertEqual(self.poll([]), [])

    def test_pending_and_annotation_changes_do_not_renotify(self):
        self.assertEqual(self.poll([alert("/study-plan", "pending")]), [])
        first = alert("/study-plan")
        self.poll([first])
        first["annotations"]["summary"] = "Updated summary"
        self.assertEqual(self.poll([first]), [])

    def test_poll_failure_does_not_resolve_alert(self):
        self.poll([alert("/study-plan")])
        with patch.object(app.urllib.request, "urlopen", side_effect=OSError("offline")), \
                patch.object(app, "announce") as send:
            app.poll_prometheus()
            send.assert_not_called()
        self.assertEqual(self.poll([alert("/study-plan")]), [])

    def test_api_error_does_not_resolve_alert(self):
        self.poll([alert("/study-plan")])
        with patch.object(app.urllib.request, "urlopen", return_value=io.StringIO(
                json.dumps({"status": "error", "error": "unavailable"}))), \
                patch.object(app, "announce") as send:
            app.poll_prometheus()
            send.assert_not_called()
        self.assertEqual(self.poll([alert("/study-plan")]), [])

    def test_label_order_does_not_change_identity(self):
        first = alert("/study-plan")
        self.poll([first])
        first["labels"] = dict(reversed(list(first["labels"].items())))
        self.assertEqual(self.poll([first]), [])

    def test_first_chat_can_send_confirmation_without_locking_again(self):
        class NonReentrantLock:
            held = False

            def __enter__(self):
                if self.held:
                    raise RuntimeError("recipient lock reacquired during notification")
                self.held = True

            def __exit__(self, *args):
                self.held = False

        update = {"result": [{"update_id": 1, "message": {
            "chat": {"id": 123, "type": "private"}}}]}
        with patch.object(app, "_lock", NonReentrantLock()), \
                patch.object(app, "_chat_id", None), \
                patch.object(app, "_last_update_id", 0), \
                patch.object(app, "save_state"), \
                patch.object(app, "tg", side_effect=[update, {"ok": True}]) as tg:
            app.discover_chat()
            self.assertEqual(app._chat_id, 123)
            self.assertEqual(tg.call_args_list[-1].args[0], "sendMessage")


if __name__ == "__main__":
    unittest.main()
