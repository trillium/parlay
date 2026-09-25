"""Tests for agent_mail (no live server: the transport is injected)."""
import json
import unittest

from agent_mail import Client, MailError

ENDPOINT = "http://127.0.0.1:8338/v0/groups/agent-mail/mcp"
PREFIX = "agent-mail-pilot__"


def sse(result, sid="sid-1"):
    return FakeResp(result, sid)


def sse_call(payload, sid="sid-1"):
    return sse({"content": [{"type": "text",
                              "text": json.dumps(payload)}]}, sid)


class FakeResp:
    def __init__(self, payload, sid="sid-1", fail_notify=False):
        self.payload = payload
        self.headers = {"Mcp-Session-Id": sid} if sid else {}
        self.fail_notify = fail_notify

    def __enter__(self):
        return self

    def __exit__(self, *a):
        return False

    def read(self):
        envelope = {"jsonrpc": "2.0", "id": 1, "result": self.payload}
        return ("event: message\ndata: %s\n\n" %
                json.dumps(envelope)).encode()


class FakeTransport:
    """Canned MCP server behind the group tool prefix; records requests."""

    def __init__(self, inbox_rows=None, send_error=None):
        self.requests = []
        self.inbox_rows = inbox_rows if inbox_rows is not None else []
        self.send_error = send_error

    def __call__(self, req, timeout=None):
        body = json.loads(req.data.decode())
        self.requests.append(body)
        method, params = body["method"], body.get("params", {})
        if method == "initialize":
            return FakeResp({"protocolVersion": "2025-06-18",
                             "serverInfo": {"name": "fake", "version": "0"}})
        if method == "notifications/initialized":
            return FakeResp({})
        if method == "tools/list":
            return sse({"tools": [
                {"name": PREFIX + n} for n in
                ("health_check", "fetch_inbox", "send_message",
                 "reply_message", "acknowledge_message",
                 "list_contacts")]})
        assert method == "tools/call", method
        name, args = params["name"], params.get("arguments", {})
        assert name.startswith(PREFIX), name  # group requires the prefix
        short = name[len(PREFIX):]
        if short == "fetch_inbox":
            assert args["unread_only"] is True
            assert args.get("registration_token") == "tok"
            return sse_call(self.inbox_rows)
        if short == "acknowledge_message":
            assert args.get("registration_token") == "tok"
            return sse_call({"message_id": args["message_id"],
                             "acknowledged": True})
        if short == "list_contacts":
            assert args["agent_name"] == "cli-beta"
            assert args.get("registration_token") == "tok"
            return sse_call({"result": [{"to": "cli-alpha",
                                           "status": "approved"}]})
        if short == "send_message":
            if self.send_error:
                return sse_call(self.send_error)
            return sse_call({"id": "msg-7", "subject": args["subject"]})
        if short == "health_check":
            return sse_call({"status": "ok"})
        if short == "list_window_identities":
            return sse_call([{"name": "cli-alpha"}, {"name": "cli-beta"}])
        raise AssertionError("unexpected tool " + name)


def make_client(transport):
    return Client(endpoint=ENDPOINT, timeout=5, opener=transport)


MSG = {"id": "msg-1", "from": "cli-alpha", "subject": "hi",
       "body_md": "hello"}


class TestInbox(unittest.TestCase):
    def test_unread_fetch_returns_messages(self):
        t = FakeTransport(inbox_rows=[MSG])
        msgs = make_client(t).inbox("/tmp/probe", "cli-beta", token="tok")
        self.assertEqual(len(msgs), 1)
        self.assertEqual(msgs[0]["subject"], "hi")
        call = [r for r in t.requests
                if r.get("method") == "tools/call"][0]
        self.assertEqual(call["params"]["name"], PREFIX + "fetch_inbox")
        self.assertEqual(call["params"]["arguments"]["agent_name"], "cli-beta")

    def test_empty_inbox(self):
        c = make_client(FakeTransport()).inbox("/p", "s", token="tok")
        self.assertEqual(c, [])


class TestSeats(unittest.TestCase):
    def test_seats_lists_reachable_pool(self):
        rows = make_client(FakeTransport()).seats("/p", "cli-beta",
                                                   token="tok")
        self.assertEqual([r["to"] for r in rows], ["cli-alpha"])


class TestSend(unittest.TestCase):
    def test_send_builds_request_and_returns_id(self):
        t = FakeTransport()
        out = make_client(t).send("/tmp/probe", "cli-beta", ["cli-alpha"],
                                  "ping", "hello")
        self.assertEqual(out["id"], "msg-7")
        call = [r for r in t.requests
                if r.get("method") == "tools/call"][0]
        args = call["params"]["arguments"]
        self.assertEqual(call["params"]["name"], PREFIX + "send_message")
        self.assertEqual(args["sender_name"], "cli-beta")
        self.assertEqual(args["to"], ["cli-alpha"])
        self.assertEqual(args["body_md"], "hello")


class TestAck(unittest.TestCase):
    def test_ack_and_no_redelivery(self):
        t = FakeTransport(inbox_rows=[MSG])
        out = make_client(t).ack("/tmp/probe", "cli-beta", "msg-1",
                                  token="tok")
        self.assertTrue(out["acknowledged"])
        t.inbox_rows = []  # server state after ack
        self.assertEqual(make_client(t).inbox("/tmp/probe", "cli-beta",
                                              token="tok"), [])


class TestErrors(unittest.TestCase):
    def test_connection_refused_names_endpoint(self):
        bad = "http://127.0.0.1:1/mcp"  # closed port, nothing needed live
        with self.assertRaises(MailError) as cm:
            Client(endpoint=bad, timeout=2).status()
        self.assertIn(bad, str(cm.exception))

    def test_unknown_tool_is_readable(self):
        with self.assertRaises(MailError):
            make_client(FakeTransport()).call("nope_not_a_tool", {})

    def test_server_text_error_is_a_failure(self):
        t = FakeTransport(send_error="Error calling tool 'send_message': "
                          "Contact approval required for: cli-alpha.")
        with self.assertRaises(MailError) as cm:
            make_client(t).send("/p", "s", ["cli-alpha"], "sub", "b")
        self.assertIn("Contact approval", str(cm.exception))


if __name__ == "__main__":
    unittest.main()
