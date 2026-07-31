"""Shared helpers for the three Devices sub-tabs (#526 wave 3).

`tabs/fleet.py` was one 59-panel module and owned these outright. Decision 5 split
it into Inventory & Hygiene, Posture & Security and Connectivity & Routing, and the
three parallel lanes that built those modules could not share a file — one file, one
owner — so each carried its own copy. They were byte-identical, which is exactly the
state that rots: a fix to the drill-down URL or the per-device prerequisite sentence
lands in one copy, and the other two keep the old behaviour while looking correct.

Nothing here builds a panel or a row. It is the vocabulary the three modules share:
the scope selector every device query is filtered by, the two field-override helpers
their tables use, and the empty-state prose that must read identically wherever a
device panel has no series.
"""

# Scope selector. tailnet and provider are real per-series metric labels (roadmap
# item L), so they filter with a plain matcher and no target_info join. Under "All"
# both expand to `.*`, which also matches a series carrying no such label at all, so
# a single-tailnet deployment reads exactly as it did before they existed.
TP = 'tailscale_tailnet=~"$tailnet", tailscale2otel_provider=~"$provider"'

# The per-device families are all gated behind one config key, and absence means the
# field was never collected rather than "none found" — so no panel reading them
# zero-fills (#385).
NO_PER_DEVICE = ("No per-device series — needs cardinality.per_entity.device, and a "
                 "control plane that reports this field.")


def host_drilldown(field="Host"):
    """Field override putting a drill-down link on a host column (#392).

    The URL is a bare query string on purpose: it resolves against whichever dashboard is
    open, so it survives the `--tab` preview builds and any `--uid` the artifact is
    generated under. A `d/<uid>` path would pin it to one.
    """
    return {"matcher": {"id": "byName", "options": field},
            "properties": [{"id": "links", "value": [
                {"title": "Scope this dashboard to ${__value.text}",
                 "url": "?var-host_name=${__value.text}&${__url_time_range}",
                 "targetBlank": False}]}]}


def flag_map(field, mapping):
    """Field override applying a semantic 0/1 value map to one table column.

    Table columns cannot use the panel-level `mappings` slot (each column needs its own
    polarity), so they go through overrides — which also keeps them out of
    test_semantic_maps.py's panel-level inventory.
    """
    return {"matcher": {"id": "byName", "options": field},
            "properties": [{"id": "mappings", "value": mapping}]}
