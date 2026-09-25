"""Signal -> Parlay wake bridge for agent-mail (fleet pool).

Watches the mail server's signal directory. A signal file
  <signals>/projects/<slug>/agents/<Seat>.signal
means <Seat> has unfetched mail (fetch_inbox deletes it). For each
present signal, this daemon sends one Parlay wake message to the seat
owner's live pane and records it in a state file, so a pane is never
poked twice for the same mail. When the signal disappears (fetched),
the state entry is cleared and the seat becomes re-armable.

Seat -> Parlay-listener mapping lives OUTSIDE the repo (pane ids are
ephemeral): a JSON file mapping each seat to its owner's pane AND that
pane's poke prefix, e.g.
  {"ChartreuseTower": {"pane": "some-pane-id", "poke": "INBOX_POKE"}}
A bare string value ({"ChartreuseTower": "some-pane-id"}) means the
default INBOX_POKE prefix. The prefix is load-bearing, not cosmetic:
the pi-inbox-bridge wakes a pane ONLY on wire lines starting with
`<POKE> v1:` (isInboxPoke) - a poke without the pane's own prefix is
parsed as chatter and the worker never wakes. That mismatch was the
broken wake-agent surface: the first version of this script sent a
prefix-less human-readable line and connected panes slept through mail.
Seats missing from the map are logged, never broadcast, never guessed.

Run (host-local, reversible):
    python3 agent-mail-notify.py --map /tmp/mailpilot/notify-map.json &
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


DEFAULT_POKE = "INBOX_POKE"


def resolve_target(entry):
    """Map entry -> (pane_id or None, poke prefix).

    Accepts {"pane": ..., "poke": ...} objects (preferred: the pane's
    own connected-store prefix) and bare pane-id strings (default prefix).
    Anything else is unmapped: log, never guess.
    """
    if isinstance(entry, dict):
        pane = entry.get("pane")
        poke = entry.get("poke") or DEFAULT_POKE
        return (pane or None, poke)
    if isinstance(entry, str) and entry:
        return entry, DEFAULT_POKE
    return None, DEFAULT_POKE


def poke(pane_id, text):
    r = subprocess.run(
        ["parlay", "send", "--agent", pane_id, text],
        capture_output=True, text=True, timeout=60,
    )
    return r.returncode == 0, (r.stderr or r.stdout).strip()[:200]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--signals", default="/tmp/mailpilot/pilot-data/signals")
    ap.add_argument("--map", default="/tmp/mailpilot/notify-map.json")
    ap.add_argument("--state", default="/tmp/mailpilot/notify-state.json")
    ap.add_argument("--once", action="store_true")
    args = ap.parse_args()

    while True:
        seat_map = load_json(args.map, {})
        state = load_json(args.state, {})
        seen = set()
        for slug, seat, path, mtime in iter_signals(args.signals):
            key = f"{slug}/{seat}"
            seen.add(key)
            pane_id = seat_map.get(seat)
            if not pane_id:
                print(f"unmapped seat with mail: {key} (no poke sent)", flush=True)
                continue
            last = state.get(key, 0)
            if mtime <= last:
                continue  # already poked for this mail
            pane_id, poke_prefix = resolve_target(seat_map.get(seat))
            ok, detail = poke(
                pane_id,
                f"{poke_prefix} v1: agent-mail for {seat} (project {slug}) "
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
