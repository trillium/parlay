// CSS part 5: speech playback (highlight, replay dots, transport, flag),
// monospace text surfaces, inline images + attach
export const CSS_SPEECH = `
  /* Message currently being spoken aloud */
  .pa-bubble.pa-speaking { box-shadow: 0 0 0 2px color-mix(in srgb, var(--pa-amber) 55%, transparent); animation: pa-speak-pulse 1.6s ease-in-out infinite; position: relative; }
  /* The specific sentence being spoken right now */
  .pa-sb.pa-speaking-block { background: color-mix(in srgb, var(--pa-amber) 20%, transparent); border-radius: 3px; }
  /* Mispronunciation flag — visible while the bubble is speaking, and on hover after */
  .pa-flag { position: absolute; top: -10px; right: -8px; width: 22px; height: 22px; padding: 0; border-radius: 50%; cursor: pointer; background: var(--pa-surf); border: 1px solid var(--pa-border); font-size: 11px; line-height: 1; display: none; }
  .pa-bubble.pa-speaking .pa-flag, .pa-bubble:hover .pa-flag { display: block; }
  .pa-flag:active { transform: scale(1.2); }
  @keyframes pa-speak-pulse { 0%,100% { box-shadow: 0 0 0 2px color-mix(in srgb, var(--pa-amber) 55%, transparent); } 50% { box-shadow: 0 0 0 3px color-mix(in srgb, var(--pa-amber) 25%, transparent); } }
  /* Replay dots + per-reply transport (#18). Dots hang OUTSIDE the bubble in
     the avatar gap (absolute, zero horizontal room in the content area). */
  .pa-block { position: relative; display: block; }
  .pa-block + .pa-block { margin-top: 2px; }
  .pa-dot-btn {
    position: absolute; left: -26px; top: 3px;
    width: 16px; height: 16px; padding: 0; cursor: pointer;
    background: none; border: none;
  }
  .pa-dot-btn::before {
    content: ''; display: block; width: 9px; height: 9px; margin: 3px auto;
    border-radius: 50%;
    background: color-mix(in srgb, var(--pa-green) 25%, transparent);
    border: 1px solid color-mix(in srgb, var(--pa-green) 60%, transparent);
  }
  .pa-dot-btn:hover::before { background: var(--pa-green); }
  .pa-block .pa-sb { display: block; white-space: pre-wrap; }
  .pa-block:has(.pa-sb.pa-speaking-block) .pa-dot-btn::before { background: var(--pa-amber); border-color: var(--pa-amber); }
  .pa-block-ctl { display: flex; gap: 8px; margin-top: 6px; align-items: center; }
  .pa-playpause { width: 26px; height: 22px; padding: 0; border-radius: 6px; cursor: pointer; background: color-mix(in srgb, var(--pa-green) 12%, transparent); border: 1px solid color-mix(in srgb, var(--pa-green) 40%, transparent); color: var(--pa-green); font-size: 10px; line-height: 1; }
  .pa-block-ctl .pa-flag { position: static; display: inline-block; width: 22px; height: 22px; }
  /* Monospace text surfaces — cursorless prep (#18): predictable char metrics */
  #pa-input { font-family: var(--pa-mono); }
  .pa-bubble.user { font-family: var(--pa-mono); font-size: 11.5px; }
  /* Inline images (#17): thumbnails under bubbles, tap for full size */
  .pa-imgs { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 6px; }
  .pa-img { max-height: 240px; max-width: 100%; border-radius: 10px; display: block; border: 1px solid var(--pa-border); cursor: pointer; }
  #pa-attach { flex-shrink: 0; width: 34px; height: 34px; border-radius: 8px; cursor: pointer; background: none; border: 1px solid var(--pa-border); color: var(--pa-muted); font-size: 14px; line-height: 1; align-self: flex-end; }
  #pa-attach:hover, #pa-attach:active { color: var(--pa-body); border-color: var(--pa-muted); }
  /* Pending attachment chips (#17 addendum): queued above the input until send */
  #pa-attach-strip { display: none; gap: 6px; padding: 6px 10px 0; flex-wrap: wrap; }
  #pa-attach-strip.visible { display: flex; }
  .pa-chip { position: relative; display: inline-block; }
  .pa-chip img { height: 48px; border-radius: 8px; border: 1px solid var(--pa-border); display: block; }
  .pa-chip-x { position: absolute; top: -6px; right: -6px; width: 18px; height: 18px; padding: 0; border-radius: 50%; cursor: pointer; background: var(--pa-surf); border: 1px solid var(--pa-border); color: var(--pa-muted); font-size: 9px; line-height: 1; }
  .pa-chip-x:hover { color: var(--pa-red); border-color: var(--pa-red); }
  /* Lightbox (#17 amendment) */
  #pa-lightbox { position: fixed; inset: 0; z-index: 10050; background: rgba(0,0,0,.88); display: flex; align-items: center; justify-content: center; }
  #pa-lightbox img { max-width: 94vw; max-height: 94vh; border-radius: 6px; transition: transform .15s ease; cursor: zoom-in; }
  #pa-lightbox img.zoomed { cursor: zoom-out; }
  #pa-lightbox-x { position: absolute; top: max(14px, env(safe-area-inset-top)); right: 16px; width: 36px; height: 36px; border-radius: 50%; cursor: pointer; background: rgba(255,255,255,.08); border: 1px solid rgba(255,255,255,.25); color: #fff; font-size: 15px; line-height: 1; z-index: 1; }
`
