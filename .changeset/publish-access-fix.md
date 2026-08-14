---
"@parlay/input": patch
"parlay-input": patch
---

Fix publish access and dependency for npm release

- Set `access: "public"` in `.changeset/config.json` so scoped packages publish publicly
- Add `publishConfig: { access: "public" }` to both manifests as belt-and-suspenders
- Change `parlay-input`'s `@parlay/input` dependency from `workspace:*` to `^0.1.0` — changeset publish does not rewrite Bun workspace:* protocol, so the concrete range is required for the published alias to install correctly
- Add MIT `LICENSE` files to both packages (npm auto-includes them but they must exist on disk)
