"""Pilot proof: complete send-receive-ack loop between two test agents over MCP Streamable HTTP."""
import json
import sys
import urllib.request

BASE = "http://127.0.0.1:18765/mcp"  # pilot server; adjust host/port to match serve-http
PROJECT = "/tmp/mailpilot/pilot-work"  # absolute path-like project key; need not exist on disk
HEADERS = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
SESSION = {}


def rpc(method, params=None, rid=1):
    body = json.dumps({"jsonrpc": "2.0", "id": rid, "method": method, "params": params or {}}).encode()
    req = urllib.request.Request(BASE, data=body, headers={**HEADERS, **SESSION})
    with urllib.request.urlopen(req, timeout=30) as resp:
        sid = resp.headers.get("Mcp-Session-Id")
        if sid:
            SESSION["Mcp-Session-Id"] = sid
        raw = resp.read().decode()
    # SSE framing: "event: message\ndata: {...}"
    for line in raw.splitlines():
        if line.startswith("data: "):
            return json.loads(line[len("data: "):])
    return json.loads(raw)


def call_tool(name, args, rid):
    out = rpc("tools/call", {"name": name, "arguments": args}, rid)
    if "error" in out:
        print(f"TOOL {name} ERROR: {json.dumps(out['error'])[:500]}")
        sys.exit(1)
    res = out.get("result", out)
    content = res.get("content") if isinstance(res, dict) else None
    if content:
        try:
            text = content[0]["text"]
        except (KeyError, IndexError, TypeError):
            print(f"TOOL {name} ERROR: unexpected result shape: {json.dumps(res)[:500]}")
            sys.exit(1)
    else:
        text = json.dumps(res)
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        return text


def inbox_messages(payload):
    if isinstance(payload, list):
        return payload
    if isinstance(payload, dict):
        for key in ("data", "result"):
            items = payload.get(key)
            if isinstance(items, list):
                return items
    print(f"INBOX ERROR: unexpected inbox shape: {json.dumps(payload)[:500]}")
    sys.exit(1)


def message_id(msg):
    mid = msg.get("id") if isinstance(msg, dict) else None
    if not mid:
        print(f"INBOX ERROR: message missing id: {json.dumps(msg)[:300]}")
        sys.exit(1)
    return mid


rid = 100
init = rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                          "clientInfo": {"name": "pilot-proof", "version": "1"}}, rid)
info = init.get("result", {}).get("serverInfo") if isinstance(init, dict) else None
if info is None:
    print(f"INIT ERROR: unexpected initialize shape: {json.dumps(init)[:500]}")
    sys.exit(1)
print("INIT server:", info)
rid += 1
# notifications/initialized (no response expected)
try:
    rpc("notifications/initialized", {}, rid)
except Exception as e:
    print("initialized-notify (ignored):", e)
rid += 1

print("PROJECT:", call_tool("ensure_project", {"human_key": PROJECT}, rid)); rid += 1
alpha = call_tool("register_agent", {"project_key": PROJECT, "name": "pilot-alpha",
                                     "program": "pi", "model": "test"}, rid); rid += 1
print("ALPHA:", json.dumps(alpha)[:300])
beta = call_tool("register_agent", {"project_key": PROJECT, "name": "pilot-beta",
                                    "program": "pi", "model": "test"}, rid); rid += 1
print("BETA:", json.dumps(beta)[:300])

sent = call_tool("send_message", {"project_key": PROJECT, "sender_name": "pilot-alpha",
                                  "to": ["pilot-beta"], "subject": "pilot ping",
                                  "body_md": "hello from alpha (pilot proof)",
                                  "ack_required": True}, rid); rid += 1
print("SENT:", json.dumps(sent)[:400])

inbox = call_tool("fetch_inbox", {"project_key": PROJECT, "agent_name": "pilot-beta",
                                  "unread_only": True, "include_bodies": True}, rid); rid += 1
print("INBOX beta:", json.dumps(inbox)[:600])
msgs0 = inbox_messages(inbox)
if not msgs0:
    print("INBOX ERROR: beta inbox empty before ack")
    sys.exit(1)
msg_id = message_id(msgs0[0])

ack = call_tool("acknowledge_message", {"project_key": PROJECT, "agent_name": "pilot-beta",
                                        "message_id": msg_id}, rid); rid += 1
print("ACK:", json.dumps(ack)[:300])
if not (isinstance(ack, dict) and ack.get("acknowledged")):
    print(f"ACK ERROR: message not acknowledged: {json.dumps(ack)[:300]}")
    sys.exit(1)

inbox2 = call_tool("fetch_inbox", {"project_key": PROJECT, "agent_name": "pilot-beta",
                                   "unread_only": True}, rid); rid += 1
print("INBOX beta after ack (expect []):", json.dumps(inbox2)[:200])
if inbox_messages(inbox2):
    print(f"ACK ERROR: beta inbox not empty after ack: {json.dumps(inbox2)[:300]}")
    sys.exit(1)

reply = call_tool("reply_message", {"project_key": PROJECT, "sender_name": "pilot-beta",
                                    "message_id": msg_id, "body_md": "pong from beta"}, rid); rid += 1
print("REPLY:", json.dumps(reply)[:400])

inbox3 = call_tool("fetch_inbox", {"project_key": PROJECT, "agent_name": "pilot-alpha",
                                   "unread_only": True, "include_bodies": True}, rid); rid += 1
print("INBOX alpha:", json.dumps(inbox3)[:600])
msgs = inbox_messages(inbox3)
if not msgs:
    print("INBOX ERROR: alpha inbox empty, expected beta reply")
    sys.exit(1)
ack2 = call_tool("acknowledge_message", {"project_key": PROJECT, "agent_name": "pilot-alpha",
                                         "message_id": message_id(msgs[0])}, rid); rid += 1
print("ACK2:", json.dumps(ack2)[:300])
if not (isinstance(ack2, dict) and ack2.get("acknowledged")):
    print(f"ACK ERROR: reply not acknowledged: {json.dumps(ack2)[:300]}")
    sys.exit(1)
print("PILOT LOOP COMPLETE")
