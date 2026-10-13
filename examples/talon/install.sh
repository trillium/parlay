#!/usr/bin/env bash
# Link the Parlay slide actions into Talon's user directory.
#   examples/talon/install.sh            # ~/.talon/user/parlay_slides
#   TALON_USER_DIR=/elsewhere examples/talon/install.sh
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
user_dir="${TALON_USER_DIR:-$HOME/.talon/user}"

if [ ! -d "$user_dir" ]; then
  echo "error: $user_dir does not exist. Install and run Talon Voice once first (https://talonvoice.com)," >&2
  echo "       or set TALON_USER_DIR to your Talon user directory." >&2
  exit 1
fi

dest="$user_dir/parlay_slides"
mkdir -p "$dest"
for f in parlay_slides.py parlay_slides.talon; do
  ln -sfn "$here/$f" "$dest/$f"
done
echo "linked $here/parlay_slides.{py,talon} -> $dest"
echo "Talon hot-reloads on change. Check:  ~/.talon/.venv/bin/repl  then  actions.user.slide_next()"
