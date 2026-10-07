// bun test preload: register happy-dom so React's `document`/`window` exist for
// the component tests under `bun test` — the same preload pattern
// packages/input and packages/client use.
//
// IS_REACT_ACT_ENVIRONMENT is what tells React it is running under a test
// harness, so `act(...)` works and state updates flush inside it instead of
// warning and racing. Without it every interaction in App.test.tsx is
// "not configured to support act(...)" — the warning is not cosmetic: the
// update may not be flushed by the time the assertion runs.
import { GlobalRegistrator } from '@happy-dom/global-registrator'

GlobalRegistrator.register()

;(globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
