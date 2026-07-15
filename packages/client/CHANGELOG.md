# @parlay/client

## 0.1.1

### Patch Changes

- Fix mobile chat input row: attach button now sits inline with the textarea and send button instead of wrapping to its own line (the row was never a flex container).
- Fix stranded client: fall back to the local command pass when server-eval returns disabled/error/unreachable, so a stale-cached serverEvalEnabled can never leave clear/submit/tab silently dead.
