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
    res = out["result"]
    # content[0].text holds JSON string
    text = res["content"][0]["text"] if res.get("content") else json.dumps(res)
    try:
        return json.loads(text)
    except (json.JSONDecodeError, KeyError):
        return text


rid = 100
init = rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                          "clientInfo": {"name": "pilot-proof", "version": "1"}}, rid)
print("INIT server:", init["result"]["serverInfo"])
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
msg_id = inbox[0]["id"] if isinstance(inbox, list) else inbox["data"][0]["id"]

ack = call_tool("acknowledge_message", {"project_key": PROJECT, "agent_name": "pilot-beta",
                                        "message_id": msg_id}, rid); rid += 1
print("ACK:", json.dumps(ack)[:300])

inbox2 = call_tool("fetch_inbox", {"project_key": PROJECT, "agent_name": "pilot-beta",
                                   "unread_only": True}, rid); rid += 1
print("INBOX beta after ack (expect []):", json.dumps(inbox2)[:200])

reply = call_tool("reply_message", {"project_key": PROJECT, "sender_name": "pilot-beta",
                                    "message_id": msg_id, "body_md": "pong from beta"}, rid); rid += 1
print("REPLY:", json.dumps(reply)[:400])

inbox3 = call_tool("fetch_inbox", {"project_key": PROJECT, "agent_name": "pilot-alpha",
                                   "unread_only": True, "include_bodies": True}, rid); rid += 1
print("INBOX alpha:", json.dumps(inbox3)[:600])
msgs = inbox3 if isinstance(inbox3, list) else inbox3.get("data", inbox3.get("result", []))
ack2 = call_tool("acknowledge_message", {"project_key": PROJECT, "agent_name": "pilot-alpha",
                                         "message_id": msgs[0]["id"]}, rid); rid += 1
print("ACK2:", json.dumps(ack2)[:300])
print("PILOT LOOP COMPLETE")
