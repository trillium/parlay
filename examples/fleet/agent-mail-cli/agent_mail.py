"""agent_mail: stdlib-only MCP client for the agent-mail tool group.

Speaks MCP Streamable-HTTP (JSON-RPC) to the group endpoint, e.g.
http://127.0.0.1:8338/v0/groups/agent-mail/mcp. Tool names are resolved
per session: an exact match wins, otherwise the unique tool whose name
ends with "__<short>" (the gateway prefixes tools with the downstream
registration name, e.g. "agent-mail-pilot__health_check").

The transport is injectable for tests: pass opener(req, timeout=...)
returning a context manager with .headers (mapping with .get) and
.read() -> bytes. The default opener is urllib.request.urlopen.
"""
import json
import urllib.error
import urllib.request

DEFAULT_ENDPOINT = "http://127.0.0.1:8338/v0/groups/agent-mail/mcp"
PROTOCOL_VERSION = "2025-06-18"
CLIENT_INFO = {"name": "agent-mail-cli", "version": "1"}


class MailError(Exception):
    """Any agent-mail failure: message is human-readable, names the leg."""


def _extract_payload(result):
    """Unwrap a tools/call result envelope into plain JSON data."""
    if not isinstance(result, dict):
        return result
    content = result.get("content")
    if isinstance(content, list) and content:
        text = content[0].get("text", "") if isinstance(content[0], dict) else ""
        try:
            return json.loads(text)
        except (ValueError, TypeError):
            return text
    if isinstance(content, list):  # empty content: e.g. an empty inbox
        sc = result.get("structuredContent")
        if isinstance(sc, dict) and "result" in sc:
            return sc["result"]
        return []
    sc = result.get("structuredContent")
    return sc if sc is not None else result


def _as_list(payload):
    """Normalize inbox/seats payloads (list or {data|result: [...]}) to a list."""
    if isinstance(payload, list):
        return payload
    if isinstance(payload, dict):
        sc = payload.get("structuredContent")
        if isinstance(sc, dict) and isinstance(sc.get("result"), list):
            return sc["result"]
        for key in ("data", "result", "messages", "agents", "identities"):
            if isinstance(payload.get(key), list):
                return payload[key]
    raise MailError("unexpected response shape: %s" % json.dumps(payload)[:200])


class Client:
    def __init__(self, endpoint=DEFAULT_ENDPOINT, token=None, timeout=20,
                 opener=None):
        self.endpoint = endpoint or DEFAULT_ENDPOINT
        self.token = token
        self.timeout = timeout
        self._opener = opener or urllib.request.urlopen
        self._session = {}
        self._tools = None
        self._rid = 1

    def _rpc(self, method, params):
        body = json.dumps({"jsonrpc": "2.0", "id": self._rid, "method": method,
                           "params": params or {}}).encode()
        self._rid += 1
        headers = {"Content-Type": "application/json",
                   "Accept": "application/json, text/event-stream"}
        headers.update(self._session)
        if self.token:
            headers["Authorization"] = "Bearer %s" % self.token
        req = urllib.request.Request(self.endpoint, data=body, headers=headers)
        try:
            with self._opener(req, timeout=self.timeout) as resp:
                sid = resp.headers.get("Mcp-Session-Id")
                if sid:
                    self._session["Mcp-Session-Id"] = sid
                raw = resp.read().decode()
        except urllib.error.HTTPError as e:
            try:
                detail = e.read().decode(errors="replace")[:200]
            except (OSError, ValueError):
                detail = ""
            detail = (": %s" % detail) if detail.strip() else ""
            raise MailError("cannot reach %s: HTTP %s%s" % (
                self.endpoint, e.code, detail))
        except urllib.error.URLError as e:
            raise MailError("cannot reach %s: %s" % (self.endpoint, e.reason
                             if hasattr(e, "reason") else e))
        except OSError as e:
            raise MailError("cannot reach %s: %s" % (self.endpoint, e))
        for line in raw.splitlines():
            if line.startswith("data: "):
                try:
                    return json.loads(line[len("data: "):])
                except ValueError:
                    continue
        try:
            return json.loads(raw)
        except ValueError:
            raise MailError("bad response from %s: %r" % (self.endpoint,
                                                           raw[:200]))

    def _ensure_session(self):
        if "Mcp-Session-Id" in self._session:
            return
        out = self._rpc("initialize", {"protocolVersion": PROTOCOL_VERSION,
                                       "capabilities": {},
                                       "clientInfo": CLIENT_INFO})
        if not isinstance(out, dict) or "result" not in out:
            raise MailError("initialize failed at %s: %s" % (
                self.endpoint, json.dumps(out)[:200]))
        try:
            self._rpc("notifications/initialized", {})
        except MailError:
            pass  # notification acks are best-effort

    def _tool_names(self):
        if self._tools is None:
            self._ensure_session()
            out = self._rpc("tools/list", {})
            try:
                self._tools = [t["name"] for t in out["result"]["tools"]]
            except (KeyError, TypeError):
                raise MailError("tools/list failed at %s: %s" % (
                    self.endpoint, json.dumps(out)[:200]))
        return self._tools

    def _resolve(self, short):
        names = self._tool_names()
        if short in names:
            return short
        matches = [n for n in names if n == short or
                   n.endswith("__" + short)]
        if len(matches) == 1:
            return matches[0]
        if not matches:
            raise MailError("tool %r not served by %s" % (short, self.endpoint))
        raise MailError("tool %r is ambiguous at %s: %s" % (
            short, self.endpoint, ", ".join(matches)))

    def call(self, tool, args):
        self._ensure_session()
        out = self._rpc("tools/call", {"name": self._resolve(tool),
                                       "arguments": args or {}})
        if isinstance(out, dict) and "error" in out:
            err = out["error"]
            raise MailError("%s: %s" % (tool, err.get("message", err)
                                          if isinstance(err, dict) else err))
        result = out.get("result", out)
        if isinstance(result, dict) and result.get("isError"):
            payload = _extract_payload(result)
            text = payload if isinstance(payload, str) else json.dumps(
                payload, default=str)
            raise MailError("%s: %s" % (tool, text[:300]))
        payload = _extract_payload(result)
        if isinstance(payload, str) and payload.startswith("Error"):
            raise MailError("%s: %s" % (tool, payload[:300]))
        return payload

    # ---- high-level verbs (arg names match the server's tools) ----
    def status(self):
        return self.call("health_check", {})

    def _authed(self, base, token):
        if token:
            base["registration_token"] = token
        return base

    def inbox(self, project, seat, unread_only=True, include_bodies=True,
              token=None):
        return _as_list(self.call("fetch_inbox", self._authed(
            {"project_key": project, "agent_name": seat,
             "unread_only": unread_only,
             "include_bodies": include_bodies}, token)))

    def send(self, project, sender, to, subject, body, token=None):
        args = {"project_key": project, "sender_name": sender, "to": list(to),
                "subject": subject, "body_md": body, "ack_required": True}
        if token:
            args["sender_token"] = token
        return self.call("send_message", args)

    def reply(self, project, sender, message_id, body, token=None):
        args = {"project_key": project, "sender_name": sender,
                "message_id": message_id, "body_md": body}
        if token:
            args["sender_token"] = token
        return self.call("reply_message", args)

    def ack(self, project, seat, message_id, token=None):
        return self.call("acknowledge_message", self._authed(
            {"project_key": project, "agent_name": seat,
             "message_id": message_id}, token))

    def seats(self, project, seat, token=None):
        # The server exposes no project-wide agent enumeration; the
        # reachable pool for a seat is its approved contacts.
        return _as_list(self.call("list_contacts", self._authed(
            {"project_key": project, "agent_name": seat}, token)))
