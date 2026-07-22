// Registers happy-dom so `document`/`window`/`navigator` exist for DOM-touching
// modules under `bun test`. Two entry points share this one module:
//   1. bunfig.toml [test].preload — applied when CWD is packages/client.
//   2. A first-line `import '../happydom'` in each client test file — applied
//      regardless of CWD (e.g. `bun test packages/client` from the repo root,
//      where bun does NOT load the package-scoped bunfig).
// Idempotent: register() runs once even if both entry points fire in the same
// bun-test process, so double-registration never throws.
import { GlobalRegistrator } from '@happy-dom/global-registrator'

declare global {
  // eslint-disable-next-line no-var
  var __PA_HAPPYDOM_REGISTERED: boolean | undefined
}

if (!globalThis.__PA_HAPPYDOM_REGISTERED) {
  GlobalRegistrator.register()
  globalThis.__PA_HAPPYDOM_REGISTERED = true
}
