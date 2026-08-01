// Test setup: add parlay's bin/ directory to PATH so executables like
// context-reset and reincarnate are available during tests. Without this,
// tests that exercise identity --park / --submit / --complete fail because
// contextResetCmd() cannot find the binary via `command -v`.
import { join } from "path"

const binDir = join(import.meta.dir, "../../bin")
process.env.PATH = `${binDir}:${process.env.PATH ?? ""}`
