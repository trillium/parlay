# Quickstart: dictate on your phone, an action fires on your laptop

Four steps. You need a Mac running [Talon Voice](https://talonvoice.com), Go
(see `./bin/parlay-preflight` for the exact floor), and a phone on the same
[Tailscale](https://tailscale.com) tailnet (or the same Wi-Fi).

1. **Check the machine.** `./bin/parlay-preflight` — prints every problem with its `fix:` command.
2. **Install the Talon actions.** `examples/talon/install.sh` — links `slide_next` / `slide_previous`
   (and the voice commands "slide next" / "slide previous") into `~/.talon/user/`. Talon hot-reloads.
3. **Start the server where your phone can reach it.**
   ```sh
   cd packages/go-server && go run ./cmd/parlay-server -exposure tailnet
   ```
   (`-exposure lan` if you are not on Tailscale; it warns, because anyone on that network can then reach the server.)
   On first run the server mints a pairing token (`~/.parlay/pair-token`, mode 0600) — there is nothing to invent or export.
4. **Scan the QR the server prints.** It opens `http://<your-address>:4242/slide` already paired:
   tap **Next** / **Previous** and `actions.user.slide_next()` / `slide_previous()` runs in Talon.

Done: the phone now triggers an action on the laptop.

## Home screen

The slide page installs as an app named **Slides**. On an iPhone, in Safari:

1. Open the slide URL from the QR in step 4.
2. Tap the Share button.
3. Tap **Add to Home Screen**.
4. Leave the name as **Slides** and tap **Add**.
5. Open **Slides** from the home screen. It launches full screen, without Safari's toolbar.

On the tailnet (the intended use, through `tailscale serve`) the buttons need no token, so the installed app works as-is. The `#t=` token from the QR is only for using the page inside Safari off the tailnet; the installed app does not carry it over.

This works on the plain `http://` URL the QR prints. The service worker registers only on a secure origin (HTTPS or localhost); the home-screen install does not need it.

## How it stays safe

- The QR carries the token in the URL **fragment** (`#t=…`), which browsers never send to a server and the
  server never logs. The page stores it in the phone's `localStorage` and strips it from the address bar.
- The QR and token-bearing URL are printed **only when stderr is an interactive terminal** — never into a
  launchd/log file. Under launchd the banner prints the address and tells you to run the server from a terminal to scan.
- `POST /api/chat/slide` always needs `Authorization: Bearer <token>`.
- Tailnet users who front the server with `tailscale serve` can skip the token by starting the server with
  `PARLAY_TRUST_TAILSCALE_HEADER=1`: a loopback peer carrying `Tailscale-User-Login` is then accepted. Off by
  default. Safe only with `tailscale serve`, Funnel off, and no other local proxy or tunnel (cloudflared, ngrok) —
  the header is forgeable by anything that can reach the server from loopback.
- `PARLAY_SLIDE_TOKEN` still overrides the generated token. To rotate, delete `~/.parlay/pair-token` and restart.
- Never expose the server with Tailscale Funnel or any public tunnel.

## When it does not work

| Symptom | Fix |
|---|---|
| Banner says "loopback-only" | Restart with `-exposure tailnet` (or `lan`). |
| Phone cannot open the URL | Same tailnet / Wi-Fi? Server must be running on the laptop. |
| Next/Previous says `502`/`inject_failed` | The Talon actions are missing: run step 2, and check `~/.talon/.venv/bin/repl` exists. |
| `401 bad or missing bearer token` | Re-scan the QR (token changed), or clear the page's site data. |
| Tapping works but keys go nowhere | Grant Talon Accessibility permission in macOS System Settings. |

Beyond slides — chat channels, spawning agents, the panel — see the [README](../README.md) and
[`remote-input.md`](remote-input.md) for the full control plane.
