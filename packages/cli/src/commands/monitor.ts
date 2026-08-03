import { SERVER, EXIT_USAGE } from "../config"
import { die } from "../http"
import { parseArgs } from "../args"
import { helpWanted } from "../help"
import { runMonitor } from "../monitor"

export async function cmdMonitor(args: string[]) {
  return runMonitor(args, { server: SERVER, exitUsage: EXIT_USAGE, die, helpWanted, parseArgs })
}
