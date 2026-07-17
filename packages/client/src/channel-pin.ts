// Channel pin — mindful page→channel mapping with an on-purpose escape hatch.
//
// SEAM STUB. The mindful-channel-mapper fix fills this in. Today sends route via
// `toAgent = activeChannel ?? __paLavishChannel` (input.ts:sendMsg): whatever tab
// happens to be active wins, so an annotation/message from a proxied page can
// silently land on the wrong agent. This module owns:
//   - a DELIBERATE pin: a page can bind itself to a channel (the mindful map),
//     which takes precedence over active-tab drift when routing a send
//   - an ON-PURPOSE escape hatch: a conscious, reversible gesture to send
//     somewhere other than the pin (never silent — the human opts out on purpose)
//   - a visible indicator of where a send will go when a pin is in effect
//
// input.ts consults resolvePinnedChannel() when building `toAgent`;
// wireChannelPin() sets up the indicator/escape-hatch UI at startup.
// Until implemented these are no-ops: resolvePinnedChannel returns undefined
// (so routing falls back to today's activeChannel ?? __paLavishChannel) and
// wireChannelPin does nothing.

export function wireChannelPin(): void {}

// Returns the channel a send should be pinned to, or undefined to fall back to
// the existing activeChannel ?? __paLavishChannel routing.
export function resolvePinnedChannel(): string | undefined {
  return undefined
}
