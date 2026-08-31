# assets/ — private support files

The loader never scans this directory; files here take effect only when a
public definition references them (pack-spec §1.3.1) — e.g. a formula step's
`description_file = "../assets/<path>"` (which stays layer-shadowable,
pack-spec §1.3.2), an agent's `session_setup_script`, or `overlay_dir`.

Put new private scripts, prompt fragments, and overlay trees here rather than
inventing top-level directories — the pack top-level namespace is reserved
(pack-spec §1.1).
