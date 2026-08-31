# agents/ — empty until the spawn seam lands

Agent definitions arrive here from the spawn seam (task-4cfpv.9, Gas City
session runtime providers). **Do not add agents before it lands**: the spawn
seam is the sole creator of the agent record (contract §6), and an agent
declared here is an agent the reconciler will try to run.

When it lands, each agent is one directory (pack-spec §1.2.4):

```text
agents/<name>/
  agent.toml            # optional; directory name is the identity
  prompt.template.md    # preferred prompt file name
```

Rules that will bite if forgotten:

- The directory name **is** the agent name; a `name` field in `agent.toml` is
  ignored for identity. Names starting with `.` or `_` are skipped.
- Imported under binding `parlay`, agent `X` runs as `parlay.X` — address it
  by the qualified name (pack-spec §2.5).
- This file (a non-directory) is ignored by the loader and safe to keep.
