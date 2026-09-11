/**
 * Parlay pi-inbox bridge — Pi-extension entrypoint.
 *
 * Splits the bridge logic into `src/` for repo hygiene (250-line commit gate);
 * this file is the thin barrel the install copies as the extension entrypoint.
 * Install copies the whole tree so the `./src` imports resolve.
 */
export { default } from "./src/bridge";
