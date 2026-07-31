"""tab_security_identity() — the "Identity & Keys" leaf of Security & Audit (#526).

Wave 2 split of the old 49-panel tabs/security.py leaf into four sub-tabs. This one owns the
credential and identity RISK view: what is about to expire, what is over-scoped, and how long
identities and outstanding invites have been sitting around.

Content is COPIED from tabs/security.py (rows "Contact verification", "Key & access expiry
risk", "Key inventory & age", "OAuth client tag scope", "Identity & invite hygiene"), plus
THREE new panels for catalog signals that reached no panel anywhere before #526:

  * `tailscale.device.key_expiring` (log event, WARN) — per-device detail behind the
    fleet-wide key-expiry histogram.
  * `tailscale_key_scope_class_ratio` (info gauge, ALERTABLE-ONLY before this panel) — each
    credential's API privilege class, none|read|all_read|write|all.
  * `tailscale_key_tag_scope_ratio` (info gauge, ALERTABLE-ONLY before this panel) — whether
    an OAuth client's own tag restriction exists at all, none|restricted.

The last two are info gauges zero-seeded across every class per credential, so both panels
query `== 1` to get each credential's ACTUAL class rather than a wall of zeros. They are
three unrelated fields away from each other and from what the row already charts:
`tailscale_key_allowed_tags_ratio` is a COUNT of allowed tags, `tailscale.key.tags` is the
tag set a credential auto-applies, and `tailscale_key_tag_scope_ratio` is whether any
restriction exists — with `none` being the BROAD case, not the narrow one.

Scope boundary (#526 decision 10): Policy & Config owns the configuration view of keys and
OAuth apps (what exists, what scopes they hold); this tab owns the risk view (what is
expiring, what is over-scoped). A signal appearing on both is intended.

Consolidation (#526 decision 7): the single-panel "Contact verification" row is merged into
"Identity & invite hygiene" — both ungated, both about identity hygiene rather than
credentials, so the merge changes nothing about when either panel renders.

NOT merged, deliberately: the new device-key-expiring log gets its own row rather than
joining "Key & access expiry risk". It carries host_name in every record and so needs
hide_when=["pii_perdevice"]; merging would put four aggregate expiry stats that contain no
host identity at all behind host-name redaction.

Sentinel scoping (#526): all three are declared HERE at TAB scope. has_key_scopes came from
tabs/policy.py, pii_actor from tabs/security.py and pii_perdevice from tabs/fleet.py, all at
DASHBOARD scope; a row gating on a sentinel nothing declares renders PERMANENTLY hidden and is
indistinguishable on screen from a correctly-gated empty row.
"""

from builder import (autogrid_row, bargauge_opts, hq, logs_opts, loki_t, lot, organize, panel, PII,
                     pii_sentinel, prom_t, row, sentinel, stat_opts, thr, ts_custom,
                     ts_opts, WIN_SLOW)

# Scrape-transport columns, excluded from every operator-facing table (#392).
_INFRA = ["Time", "__name__", "job", "instance",
          "service_instance_id", "service_name", "service_namespace"]

_PE_KEY_EMPTY = ("No per-credential key series — needs the keys collector (collectors.keys) "
                 "and cardinality.per_entity.key.")


def tab_security_identity(scope):
    sentinel("has_key_scopes", "tailscale_key_scopes_ratio", scope)
    sentinel("has_invites_dev", "tailscale_device_invites_count_ratio", scope)
    pii_sentinel("pii_actor",
                 '(%s{category="emails"} == 0) and ignoring(category) (%s{category="user_display_names"} == 0)'
                 % (PII, PII), scope)
    # Same expression as tabs/fleet.py's DASHBOARD-scoped declaration — kept
    # byte-identical on purpose so the two never diverge into two meanings.
    pii_sentinel("pii_perdevice",
                 '(%s{category="hostnames"} == 0) and ignoring(category) (%s{category="node_ids"} == 0)'
                 % (PII, PII), scope)

    _DK = lot("tailscale_device_key_expiry_seconds", WIN_SLOW)
    _AK = lot("tailscale_key_expiry_seconds", WIN_SLOW)

    # -----------------------------------------------------------------------
    # Key & access expiry risk — from tabs/security.py. Four same-size tiles, so
    # autogrid_row (#526 decision 6). Descriptions added: the originals shipped
    # with none, which the desc= gate no longer allows.
    # -----------------------------------------------------------------------
    expiry = [
        panel("Device keys ≤7d", "stat",
              [prom_t("count((%s - time() < 7*86400) and (%s - time() > 0)) or vector(0)" % (_DK, _DK),
                      instant=True)],
              unit="short", thresholds=thr([(None, "green"), (1, "yellow"), (3, "red")]),
              options=stat_opts(color="background"),
              desc="Devices whose node key expires within 7 days and has not already expired. "
                   "Each one drops off the tailnet when it lapses unless it is re-authed or "
                   "marked as never-expiring."),
        panel("Device keys ≤30d", "stat",
              [prom_t("count((%s - time() < 30*86400) and (%s - time() > 0)) or vector(0)" % (_DK, _DK),
                      instant=True)],
              unit="short", options=stat_opts(),
              desc="Devices whose node key expires within 30 days — the early-warning "
                   "counterpart to the 7-day tile, sized for a rotation window rather than "
                   "an incident."),
        panel("API/auth keys ≤7d", "stat",
              [prom_t("count((%s - time() < 7*86400) and (%s - time() > 0)) or vector(0)" % (_AK, _AK),
                      instant=True)],
              unit="short", thresholds=thr([(None, "green"), (1, "yellow")]),
              options=stat_opts(color="background"),
              desc="Tailnet API keys and auth keys expiring within 7 days. An expiring API key "
                   "takes whatever automation holds it down with it — including this exporter."),
        panel("API/auth keys ≤30d", "stat",
              [prom_t("count((%s - time() < 30*86400) and (%s - time() > 0)) or vector(0)" % (_AK, _AK),
                      instant=True)],
              unit="short", options=stat_opts(),
              desc="Tailnet API keys and auth keys expiring within 30 days."),
    ]

    # -----------------------------------------------------------------------
    # #526: tailscale.device.key_expiring — the per-device WARN log behind the
    # fleet-wide key-expiry histogram. Own row, behind host-name redaction.
    # -----------------------------------------------------------------------
    keyexpiring = [
        (panel("Expiring device keys (log detail)", "logs",
               [loki_t("{service_name=\"tailscale2otel\"} | event_name=`tailscale.device.key_expiring` "
                       "|~ `$log_filter`", maxlines=200)],
               options=logs_opts(),
               desc="One WARN record per device whose node key expires inside the fixed 14-day warn "
                    "window and has not yet lapsed. Carries host_name, host_id and "
                    "tailscale_device_key_expires_in_days — the actionable per-device detail the "
                    "fleet-wide key-expiry histogram cannot give you. Needs the devices collector; "
                    "hidden when host-name redaction is active."), 12, 9),
        # #527: tailscale.key.expiring — the AUTH-key counterpart of the device-key
        # log beside it. It had no panel at all. It looked covered because its dotted
        # name appears as an option VALUE in the `log_event` dropdown, which the
        # coverage gate read as a reference — a string in a list, not a query, and it
        # put the signal in front of nobody. Its sibling row above charts DEVICE node
        # keys; this one is auth keys and OAuth clients, a different population with a
        # different remedy (rotate a credential, not reauthenticate a machine).
        (panel("Expiring auth keys & credentials (log detail)", "logs",
               [loki_t("{service_name=\"tailscale2otel\"} | event_name=`tailscale.key.expiring` "
                       "|~ `$log_filter`", maxlines=200)],
               options=logs_opts(),
               desc="One WARN record per auth key or OAuth credential expiring inside the configured "
                    "`expiry_warn` window. Carries the key id, type, auth kind, owner, tags and "
                    "tailscale_key_expires_in_seconds — seconds REMAINING, not an absolute "
                    "timestamp, so it counts down rather than up. Needs the keys collector. Read it "
                    "against the expiry tiles above: those say how many, this says which."), 12, 9),
    ]

    # -----------------------------------------------------------------------
    # Key inventory & age — from tabs/security.py, unchanged. Two same-size
    # panels, so autogrid_row (#526 decision 6).
    # -----------------------------------------------------------------------
    keyinv = [
        panel("Key age (p50 / p90)", "timeseries",
              [prom_t(hq("0.5", "tailscale_keys_age_seconds"), legend="p50"),
               prom_t(hq("0.9", "tailscale_keys_age_seconds"), legend="p90")],
              unit="s", custom=ts_custom(fill=10), options=ts_opts(),
              novalue="No key age data — needs the keys collector (collectors.keys). "
                      "Keys whose creation time the API omits are skipped.",
              desc="How old the tailnet's credentials are. A rising p90 means long-lived keys "
                   "nobody is rotating, which expiry panels alone cannot show."),
        panel("Keys by owner and type", "bargauge",
              [prom_t("sum by (tailscale_key_owner, tailscale_key_type) (%s)"
                      % lot("tailscale_keys_by_owner_ratio", WIN_SLOW),
                      legend="{{tailscale_key_owner}} / {{tailscale_key_type}}")],
              unit="short", options=bargauge_opts(),
              novalue="No key ownership data — needs the keys collector (collectors.keys). "
                      "Keys with no owning user are excluded.",
              desc="Who holds the tailnet's credentials, by owning user id and key type. Concentration "
                   "on one owner is a departure risk; the owner is an opaque user id, not an email."),
    ]

    # -----------------------------------------------------------------------
    # Credential scope & blast radius — tabs/security.py's "OAuth client tag scope"
    # row plus the two new privilege-class gauges. Gate unchanged:
    # present="has_key_scopes" (both new signals are gated by
    # cardinality.per_entity.key, the same prerequisite) and
    # hide_when=["pii_actor"] (tailscale_key_description is operator free text and
    # routinely names a person).
    # -----------------------------------------------------------------------
    scoperow = [
        (panel("Unrestricted OAuth clients", "stat",
               [prom_t("count(%s == 0)" % lot("tailscale_key_allowed_tags_ratio", WIN_SLOW),
                       instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"),
               novalue=_PE_KEY_EMPTY,
               desc="OAuth clients whose device-create capability is restricted to no tags at all, "
                    "so any device they register can claim any tag."), 6, 6),
        (panel("OAuth client tag restrictions", "table",
               [prom_t(lot("tailscale_key_allowed_tags_ratio", WIN_SLOW), instant=True, fmt="table")],
               transformations=[organize(
                   exclude=_INFRA,
                   rename={"tailscale_key_id": "Key", "tailscale_key_owner": "Owner",
                           "tailscale_key_description": "Description",
                           "tailscale_key_type": "Type", "Value": "Allowed tags"})],
               novalue=_PE_KEY_EMPTY,
               desc="Allowed-tag COUNT per OAuth client credential (0 is unrestricted). Distinct "
                    "from the tag-restriction CLASS panel below and from tailscale.key.tags, the "
                    "tags a credential auto-applies to devices it creates."), 18, 6),
        # #526: tailscale_key_scope_class_ratio — ALERTABLE-ONLY before this panel
        # existed, i.e. an operator could be paged by it and find nothing on any
        # dashboard. Zero-seeded across every class per credential, so `== 1` is what
        # picks out each credential's actual class instead of a wall of zeros.
        (panel("Credential privilege class (blast radius)", "table",
               [prom_t("%s == 1" % lot("tailscale_key_scope_class_ratio", WIN_SLOW),
                       instant=True, fmt="table")],
               novalue=_PE_KEY_EMPTY,
               transformations=[organize(
                   exclude=_INFRA + ["Value"],
                   rename={"tailscale_key_id": "Key", "tailscale_key_type": "Type",
                           "tailscale_key_description": "Description",
                           "tailscale_key_owner": "Owner",
                           "tailscale_key_scope_class": "Privilege class"})],
               desc="The API privilege class each credential actually holds, ranked by blast "
                    "radius: none < read < all_read < write < all. `all` is unrestricted read AND "
                    "write, including API surfaces that do not exist yet — a credential that can "
                    "do anything Tailscale later adds. The gauge is zero-seeded across every "
                    "class per credential, so this panel filters to == 1 to show the one class "
                    "that is set."), 14, 7),
        # #526: tailscale_key_tag_scope_ratio — also ALERTABLE-ONLY before this panel.
        (panel("OAuth client tag restriction class", "table",
               [prom_t("%s == 1" % lot("tailscale_key_tag_scope_ratio", WIN_SLOW),
                       instant=True, fmt="table")],
               novalue=_PE_KEY_EMPTY,
               transformations=[organize(
                   exclude=_INFRA + ["Value"],
                   rename={"tailscale_key_id": "Key", "tailscale_key_type": "Type",
                           "tailscale_key_description": "Description",
                           "tailscale_key_owner": "Owner",
                           "tailscale_key_tag_scope": "Tag scope"})],
               desc="Whether each OAuth client carries a tag restriction of its own: `none` or "
                    "`restricted`. READ THE POLARITY — `none` means NO restriction, so the "
                    "credential may create devices carrying ANY tag; it is the BROAD case, not "
                    "the narrow one. Only OAuth clients carry this field. Zero-seeded across both "
                    "classes, so this panel filters to == 1."), 10, 7),
    ]

    # -----------------------------------------------------------------------
    # Identity & invite hygiene — from tabs/security.py, with the former
    # single-panel "Contact verification" row merged in (#526 decision 7). Both
    # were ungated, so nothing about when either renders changes.
    # -----------------------------------------------------------------------
    identityage = [
        (panel("User account age (p50 / p90)", "timeseries",
               [prom_t(hq("0.5", "tailscale_users_age_seconds"), legend="p50"),
                prom_t(hq("0.9", "tailscale_users_age_seconds"), legend="p90")],
               unit="s", custom=ts_custom(fill=10), options=ts_opts(),
               novalue="No user age data — needs the users collector (collectors.users). "
                       "Users with no reported creation time are skipped.",
               desc="Age of the tailnet's user accounts. Users with no creation time are omitted "
                    "rather than reported as age zero."), 8, 6),
        (panel("Pending user-invite age (p50 / p90)", "timeseries",
               [prom_t(hq("0.5", "tailscale_user_invites_pending_age_seconds"), legend="p50"),
                prom_t(hq("0.9", "tailscale_user_invites_pending_age_seconds"), legend="p90")],
               unit="s", custom=ts_custom(fill=10), options=ts_opts(),
               novalue="No pending user-invite age data — needs the users collector "
                       "(collectors.users) and at least one emailed invite.",
               desc="Time since Tailscale last emailed each unaccepted user invite. Manual-link "
                    "invites carry no delivery timestamp and are omitted."), 8, 6),
        (panel("Pending device-invite age (p50 / p90)", "timeseries",
               [prom_t(hq("0.5", "tailscale_device_invites_pending_age_seconds"), legend="p50"),
                prom_t(hq("0.9", "tailscale_device_invites_pending_age_seconds"), legend="p90")],
               unit="s", custom=ts_custom(fill=10), options=ts_opts(),
               novalue="No pending device-invite age data — needs collect_device_invites. "
                       "Accepted invites are excluded.",
               desc="How long unaccepted device shares have been outstanding. A long tail is a "
                    "standing offer of access nobody took up and nobody revoked."), 8, 6),
        # merged in from the former single-panel "Contact verification" row.
        # No zero-fill: this row is ungated, so with the contacts collector off the
        # zero read as a green "all contacts verified" for a tailnet nobody had checked.
        (panel("Contact needs verification", "stat",
               [prom_t("max(%s)" % lot("tailscale_contact_needs_verification_ratio", WIN_SLOW),
                       instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "red")]),
               options=stat_opts(color="background"),
               novalue="No contact data — enable the contacts collector (collectors.contacts).",
               desc="Whether any tailnet contact address requires re-verification "
                    "(admin/security/billing)."), 6, 6),
    ]

    # -----------------------------------------------------------------------
    # Device share invites — ported from tabs/security.py, where it was the one row
    # the wave-3 sub-tab split accounted for nowhere and would have dropped silently.
    # It lands on the risk side (#526 decision 10) rather than with Policy & Config's
    # invite inventory: an unaccepted share that grants exit-node use, or one that can
    # be redeemed repeatedly, is standing unrevoked access to the tailnet, which is the
    # question this sub-tab answers. The three panels carry only bounded enum labels —
    # accepted, exit-node, multi-use — and no invitee identity, so the row needs no
    # redaction gate.
    # -----------------------------------------------------------------------
    devinvites = [
        (panel("Device shares: pending vs accepted", "timeseries",
               [prom_t("sum by (tailscale_device_invite_accepted) (%s)"
                       % lot("tailscale_device_invites_count_ratio"),
                       legend="accepted={{tailscale_device_invite_accepted}}")],
               unit="short", custom=ts_custom(), options=ts_opts(),
               desc="Device share invites grouped by accepted status. A pending invite is an "
                    "outstanding grant nobody has taken up yet; it stays live until it is "
                    "revoked, so a growing pending count is unretired access, not a backlog."),
         12, 6),
        (panel("Exit-node-granting shares", "stat",
               [prom_t("sum(%s) or vector(0)"
                       % lot('tailscale_device_invites_count_ratio{tailscale_device_invite_allow_exit_node="true"}'),
                       instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="background"),
               desc="Device share invites that also grant exit-node use, so the recipient can "
                    "route their traffic through the shared device. Review whether that is "
                    "intended — it is a broader grant than sharing the device itself."), 6, 6),
        (panel("Multi-use shares", "stat",
               [prom_t("sum(%s) or vector(0)"
                       % lot('tailscale_device_invites_count_ratio{tailscale_device_invite_multi_use="true"}'),
                       instant=True)],
               unit="short", thresholds=thr([(None, "green"), (1, "yellow")]),
               options=stat_opts(color="background"),
               desc="Device share invites that can be redeemed more than once, so a single "
                    "leaked link admits an unbounded number of recipients."), 6, 6),
    ]

    return [
        autogrid_row("Key & access expiry risk", expiry),
        row("Device share invites", devinvites, present="has_invites_dev"),
        row("Expiring keys & credentials", keyexpiring, hide_when=["pii_perdevice"]),
        autogrid_row("Key inventory & age", keyinv),
        row("Credential scope & blast radius", scoperow,
            present="has_key_scopes", hide_when=["pii_actor"]),
        row("Identity & invite hygiene", identityage),
    ]
