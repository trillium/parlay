# orders/ — empty until a seam needs scheduled work

Orders are Gas City's scheduled/recurring work surface: one `<name>.toml` per
order, scanned by the orders subsystem (pack-spec §1.3.5; semantics in
`engdocs/architecture/orders.md` at the pinned Gas City ref). Issue-128 §40
(task triggering) maps here, not to formulas.

Likely first tenants: liveness patrol cadence (task-4cfpv.10) or event-spool
maintenance (task-4cfpv.11). Cron schedules evaluate in the city timezone
(`[workspace].timezone`, unset here — controller-local) unless an order sets
its own.

This file is not an order and is ignored by the loader.
