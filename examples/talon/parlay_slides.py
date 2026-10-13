"""Parlay slide actions for Talon Voice.

Parlay's phone page (/slide) and the spoken "slide next" both end in
`actions.user.slide_next()` / `actions.user.slide_previous()` inside Talon
(see docs/remote-input.md). This file defines those two actions so a fresh
Talon install can run the demo. Copy or symlink it into ~/.talon/user/ -- or
run examples/talon/install.sh.

The default implementation presses Right / Left in whatever app has focus,
which advances most slide decks and web presentations. Override it from your
own Talon config (a user action defined later wins) if your deck needs
different keys.
"""

from talon import Context, Module, actions

mod = Module()
ctx = Context()


@mod.action_class
class Actions:
    def slide_next():
        """Advance to the next slide (Parlay phone trigger / voice)."""
        actions.key("right")

    def slide_previous():
        """Go back to the previous slide (Parlay phone trigger / voice)."""
        actions.key("left")
