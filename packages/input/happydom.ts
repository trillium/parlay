// bun test preload: register happy-dom so `document`/`window` exist for
// DOM-touching modules under `bun test`. Wired via bunfig.toml [test].preload.
import { GlobalRegistrator } from '@happy-dom/global-registrator'

GlobalRegistrator.register()
