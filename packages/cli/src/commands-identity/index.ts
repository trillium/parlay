// Barrel for the identity/scratchpad command surface. Keeps `./commands-identity`
// import paths stable after the split into store/say/lifecycle/mem modules.

export { cmdSay } from "./say"
export { cmdScratchpad, cmdIdentity } from "./mem"
export { agentsRoot, writeContextJson, readFrontmatter, writeFrontmatter } from "./store"
