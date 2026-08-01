---
"@parlay/client": minor
---

Add remote debug-log capture (`debug-log.ts`) and an on-screen mobile console (`mobile-console.ts`, lazy-loaded eruda, `?paConsole=1` or long-press the drawer trigger) so the captain's phone can be diagnosed without devtools. Fix `#pa-jump` ("Jump to latest") silently failing on some WebKit/iOS Safari builds: `scrollTo({behavior:'instant'})` now falls back to a plain `scrollTop` assignment if the browser throws on the non-standard `instant` value; the same fallback covers scroll-position restore on load.
