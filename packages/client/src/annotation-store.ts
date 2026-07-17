// Annotation persistence — survives page reloads.
//
// SEAM STUB. The refresh-state-persistence fix fills this in. Page annotations
// currently live only in the in-memory `annotations` array (state.ts) with live
// DOM `el` references, so a reload wipes them. This module owns:
//   - a serializable per-page record of annotations (note + a locator that can
//     re-find the target element on a fresh DOM), keyed by page identity
//   - persist-on-change (called from annotation.ts confirm/remove/clear)
//   - rehydrate-on-load, wired via initAnnotationPersistence() at startup
//
// Until implemented these are no-ops, so init.ts can call initAnnotationPersistence
// unconditionally and annotation.ts can call the persist hooks harmlessly.

export function initAnnotationPersistence(): void {}
