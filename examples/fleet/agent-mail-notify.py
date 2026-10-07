"""Signal -> Parlay wake bridge for agent-mail (fleet pool).

Watches the mail server's signal directory. A signal file
  <signals>/projects/<slug>/agents/<Seat>.signal
means <Seat> has unfetched mail (fetch_inbox deletes it). For each
present signal, this daemon sends one Parlay wake message to the seat
owner's live pane and records it in a state file, so a pane is never
poked twice for the same mail. When the signal disappears (fetched),
the state entry is cleared and the seat becomes re-armable.

Seat -> Parlay-listener mapping lives OUTSIDE the repo (pane ids are
ephemeral): a JSON file mapping each seat to its owner's pane, e.g.
  {"ChartreuseTower": {"pane": "some-pane-id"}}
A bare string value ({"ChartreuseTower": "some-pane-id"}) means the same.
A legacy "poke" key in the object form is accepted but IGNORED: every wake
emits the distinct MAIL_POKE prefix so the pi-inbox-bridge routes it to the
agent-mail prompt (MCP verbs), never the bd-store worker prompt. A store-
prefixed poke would hand the pane inbox instructions for mail — that was the
broken wake-agent surface this bridge closes.
Seats missing from the map are logged, never broadcast, never guessed.

Durable paths: every default below derives from $MAIL_HOME
($HOME/data/agent-mail when unset — see agent-mail.env.template).
No ephemeral-tmp paths: the pilot's tmp-dir pool and mesh were lost on reboot.

Run (host-local, reversible):
    python3 agent-mail-notify.py --map "$MAIL_HOME/notify-map.json" &
Teardown: kill the pid. Nothing else to undo.

YOLO_DECISION: built watcher + map-file seam now; daemon launch waits on
owner mapping (needs-user-input follow-up bead). YOLO_TRADEOFF: a watcher
with no verified mapping would only log, so shipping the script without
starting it beats theater.
"""

import argparse
import json
import os
import subprocess
import sys
import time

POLL_SECONDS = 15


def load_json(path, default):
    try:
        with open(path) as f:
            return json.load(f)
    except (OSError, ValueError):
        return default


def save_json(path, obj):
    tmp = path + ".tmp"
    with open(tmp, "w") as f:
        json.dump(obj, f)
    os.replace(tmp, path)


def iter_signals(signals_root):
    """Yield (project_slug, seat, signal_path, mtime)."""
    projects = os.path.join(signals_root, "projects")
    if not os.path.isdir(projects):
        return
    for slug in sorted(os.listdir(projects)):
        agents = os.path.join(projects, slug, "agents")
        if not os.path.isdir(agents):
            continue
        for fn in sorted(os.listdir(agents)):
            if not fn.endswith(".signal"):
                continue
            p = os.path.join(agents, fn)
            try:
                yield slug, fn[: -len(".signal")], p, os.path.getmtime(p)
            except OSError:
                continue


DEFAULT_POKE = "MAIL_POKE"

# Exact wire line the bridge expects (isMailPoke + parseMailPoke):
#   MAIL_POKE v1: agent-mail for <Seat> (project <slug>) - ...
# Emitter and bridge must agree on it verbatim.


def resolve_pane(entry):
    """Map entry -> pane_id or None.

    Accepts {"pane": ...} objects (a legacy "poke" key is ignored:
    mail wakes always emit MAIL_POKE) and bare pane-id strings.
    Anything else is unmapped: log, never guess.
    """
    if isinstance(entry, dict):
        return entry.get("pane") or None
    if isinstance(entry, str) and entry:
        return entry
    return None


def mail_home():
    """Durable mail root: $MAIL_HOME, defaulting to ~/data/agent-mail."""
    return os.environ.get("MAIL_HOME", os.path.join(os.path.expanduser("~"), "data", "agent-mail"))


def poke(pane_id, text):
    try:
        r = subprocess.run(
            ["parlay", "send", "--agent", pane_id, text],
            capture_output=True, text=True, timeout=60,
        )
    except (OSError, subprocess.SubprocessError) as e:
        return False, f"{type(e).__name__}: {e}"[:200]
    return r.returncode == 0, (r.stderr or r.stdout).strip()[:200]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--signals", default=os.path.join(mail_home(), "pilot-data", "signals"))
    ap.add_argument("--map", default=os.path.join(mail_home(), "notify-map.json"))
    ap.add_argument("--state", default=os.path.join(mail_home(), "notify-state.json"))
    ap.add_argument("--once", action="store_true")
    args = ap.parse_args()

    while True:
        seat_map = load_json(args.map, {})
        state = load_json(args.state, {})
        seen = set()
        for slug, seat, path, mtime in iter_signals(args.signals):
            key = f"{slug}/{seat}"
            seen.add(key)
            pane_id = resolve_pane(seat_map.get(seat))
            if not pane_id:
                print(f"unmapped seat with mail: {key} (no poke sent)", flush=True)
                continue
            last = state.get(key, 0)
            if mtime <= last:
                continue  # already poked for this mail
            ok, detail = poke(
                pane_id,
                f"{DEFAULT_POKE} v1: agent-mail for {seat} (project {slug}) "
                f"- fetch_inbox unread_only=true, then acknowledge.",
            )
            print(f"poke {seat} -> {pane_id}: {'sent' if ok else 'FAILED ' + detail}",
                  flush=True)
            if ok:
                state[key] = mtime
        for key in [k for k in state if k not in seen]:
            del state[key]  # signal gone = fetched; re-arm
        save_json(args.state, state)
        if args.once:
            return
        time.sleep(POLL_SECONDS)


if __name__ == "__main__":
    sys.exit(main())
