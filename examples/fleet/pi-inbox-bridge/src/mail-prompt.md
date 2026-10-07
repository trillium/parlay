Parlay agent-mail poke for seat {{seat}} (project {{project}}). You have unread mail: fetch it, act on it, then resume prior work. Repeated pokes are coalesced; do not wait for another reminder.

1. Fetch unread mail with fetch_inbox(project_key, {{seat}}, unread_only=True) and read each item with its thread context.
2. For each item: act on it or reply via reply_message (send_message only for a new thread), then acknowledge_message every item you processed so it is never redelivered.
3. If this pane is also connected to a Parlay store channel with pending work, drain it now; otherwise resume exactly what you were doing before this poke.
