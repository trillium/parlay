---
"@parlay/client": minor
---

Add a persistent hands-free `noKeyboardMode` setting (Settings ⚙ → Voice → "Hands-free (no keyboard pop-up)", default off) that suppresses composer auto-focus on drawer-open, channel/sender-picker close, page-nav open, and post-send, so the iOS on-screen keyboard never auto-pops while driving the panel by voice (Talon). Tapping the composer still focuses it deliberately. Reachable without tapping via the new `set-hands-free` device command (`args.enabled` toggles or sets explicitly).
