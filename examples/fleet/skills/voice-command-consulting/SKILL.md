---
name: voice-command-consulting
description: Interview-driven consulting for custom voice command sets. Discover workflows, translate to noun-verb commands (or verb-first for developers), iterate, and emit JSON config.
argument-hint: "What's the user's primary workflow or use case (e.g., 'private chef managing orders')?"
disable-model-invocation: false
---

# Voice Command Consulting

**INVOKE THIS**: `/voice-command-consulting "describe your workflow"`

When invoked, this skill spawns a consulting agent who will:
1. Interview you about your workflow
2. Suggest command patterns (noun-verb or verb-first based on your domain)
3. Iterate on commands with you
4. Output a JSON command definition ready to import

See `consulting-agent.md` in this skill directory for the full agent prompt.

## Workflow Discovery → Command Design → JSON Config

The consulting process flows: **interview** → **suggest** → **refine** → **export**.

### 1. Discover Workflows

Ask the user:
- **What visuals must you see?** (screens, panels, data views they reference)
- **What actions do you take?** (what changes state? what triggers notifications?)
- **When do you use voice?** (hands busy? eyes elsewhere? both?)
- **How many times daily?** (frequent = optimize for speed, rare = okay to be verbose)

Examples:
- Chef: "I need to see incoming orders and mark them ready."
- Developer: "I need to switch between open PRs and file a status update."

### 2. Design Commands (Noun-Verb Pattern)

Based on Talon Hub conventions, suggest **noun-verb** or **verb-noun** commands:
- `orders show` — reveal incoming orders
- `order <name> select` — pick an order (name is disambiguator)
- `order ready submit` — mark order as ready
- `pr list show` — show open PRs
- `pr <number> comment` — add a comment

**Key principle**: each command should advance the user toward their next action.

### 3. Build Aliases (STT Tolerance)

For each command, suggest phonetically similar variants to handle STT misrecognition:
- `bravely / gravely / briefly / beverly` (four homophones for reliability)
- `frame / brake / shame` (pick one, alias the others)

Aliases are learned via 3-utterance calibration sessions when the user trains.

### 4. Define Trigger Words

Identify a **submit trigger** word(s) to distinguish intent from action:
- User says: `order three ready` → system transitions to confirmation view
- System shows: "Ready to submit: order three marked ready"
- User says: `bravely` → system submits

This two-step flow prevents accidental submission and allows visual review.

### 5. Output JSON Config

Produce a command definition that the system can load:

```json
{
  "commands": [
    {
      "id": "order_show",
      "triggers": ["orders show", "order list show"],
      "aliases": ["or- show", "orderlist show"],
      "action": "transition",
      "target_view": "incoming_orders",
      "description": "Display incoming orders"
    },
    {
      "id": "order_select",
      "triggers": ["order {name} select", "select {name}"],
      "aliases": ["oder {name} select"],
      "action": "transition",
      "target_view": "compose",
      "context": { "selected_order": "{name}" },
      "description": "Select an order and move to compose view"
    }
  ],
  "submit_triggers": ["bravely", "gravely", "briefly", "beverly"],
  "reset_trigger": "never mind",
  "protocol": "noun-verb",
  "visual_feedback": "instant_state_update",
  "training_utterances_per_command": 3
}
```

## Refinement Loop

After initial suggestions:
- Ask: "Does this feel right? Too many commands? Missing something?"
- Test: "Say this command out loud — does it feel natural?"
- Refine: Update aliases, adjust grouping, remove low-value commands
- Validate: "Can you imagine using this 5 times per day?"

## Key Constraints

1. **STT reliability** — Good acoustic environment required (quiet workspace, good mic placement)
2. **Cognitive load** — Start with 5–8 commands max; add more via organic learning over time
3. **Muscle memory** — Commands should follow consistent patterns (noun-verb, not verb-noun)
4. **Recoverability** — Every action can be undone or corrected visually

## Anti-Patterns to Avoid

- ❌ Too many homophones that collide (e.g., "frame" + "frame" as both command and submit)
- ❌ Inconsistent structure ("orders show" + "get status" — mixing patterns)
- ❌ Chaining commands that require 5+ steps without progress
- ❌ Voice-only mode without visual feedback (chef cooking can't hear confirmations)

## Tips

- **Observe first**: Ask the user to narrate what they do, then build commands from their own language
- **Start narrow**: 3 commands for the top workflow beats 20 scattered ones
- **Test early**: Have them say the commands out loud before finalizing
- **Monitor real use**: Log misrecognitions and iterate quarterly

---

**Next step**: Invoke this skill with the user's primary workflow, and the consulting agent will guide you through the full interview → design → export cycle.
