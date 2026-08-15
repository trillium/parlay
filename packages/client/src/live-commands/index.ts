// Live commands — the panel half of parlay's live-command registry.
//
// Barrel over model.ts (wire shape + pure display rules, no DOM) and panel.ts
// (rendering, the read-endpoint fetch, wiring). Import from './live-commands';
// nothing outside this folder should reach for either file directly.
//
// See docs/live-commands.md for the registration design, the coverage limits,
// and why `parlay commands` and this view are two renderers over one registry.

export {
  type CommandInvocation, type CommandsResponse,
  COVERAGE_NOTE, UNSUPPORTED_NOTE,
  isRunning, sortCommands, liveDurationMs, commandAge, commandDetail,
} from './model'

export {
  applyCommandsSnapshot, applyCommandUpdate,
  commandRowEl, renderCommandsInto, renderLiveCommands,
  refreshLiveCommands, toggleLiveCommands, wireLiveCommandsEvents,
  _resetLiveCommandsForTests,
} from './panel'
