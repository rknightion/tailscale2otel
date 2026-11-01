"""Apply selector controls at the leaf-tab boundary, including ratio operands.

Process-only signals deliberately do not acquire a tailnet matcher: app.New/Run
emits these through procEmitter. Provider still applies (resource.constLabelAttrs).
Mixed process/runtime families (export, cardinality, queues and dedup) are scoped
when a tailnet is selected; All also retains their unlabeled process series.
Ad-hoc variables are applied by Grafana to their datasource, not interpolated here.
"""
import re

# Each exception is an emitter ownership fact, not an empty-panel workaround.
PROCESS_PREFIXES = (
    'process_',                         # runProcessReporter
    'tailscale_rdns_', 'tailscale_geoip_',  # shared resolver/database
    'tailscale_organization_',          # organization roster, primary process only
    'tailscale2otel_admin_', 'tailscale2otel_annotation_',
    'tailscale2otel_build_', 'tailscale2otel_capability_',  # runHeartbeat
    'tailscale2otel_component_',
    'tailscale2otel_config_', 'tailscale2otel_coordination_',
    'tailscale2otel_ingress_wal_', 'tailscale2otel_metrics_',
    'tailscale2otel_pii_', 'tailscale2otel_profiling_',
    'tailscale2otel_export_failures_',    # global InstallExportErrorHandler
    'tailscale2otel_receiver_', 'tailscale2otel_runtime_',
    'tailscale2otel_tls_', 'tailscale2otel_up_', 'tailscale2otel_update_',
)
# RunCheckpointReporter uses procEmitter for shared file stats. Persist errors
# are emitted by runtime schedulers/processors, so MUST retain tailnet scope.
PROCESS_METRICS = {
    'tailscale2otel_checkpoint_disk_size_bytes',
    'tailscale2otel_checkpoint_persist_age_seconds',
}
# Unitted gauges plus passthrough raw node metrics do not have a conventional
# OTLP suffix. Labels with a tailscale prefix are NOT vector selectors.
UNSUFFIXED = {
    'tailscale2otel_series_active', 'tailscale2otel_series_by_group',
    'tailscale2otel_series_limit', 'tailscale2otel_nodemetrics_discovery_targets',
    'tailscale2otel_metrics_scrape_in_flight',
    'tailscale_stream_inflight', 'tailscale_webhook_inflight',
    'tailscale_network_unique_dst_peers', 'tailscale_network_unique_dst_ports',
    'tailscale_device_routes_advertised', 'tailscale_device_routes_enabled',
    'tailscale_subnet_routes_advertised', 'tailscale_subnet_routes_enabled',
    'tailscale_subnet_routes_unapproved', 'tailscale_pam_sessions_active',
    'tailscale_service_ports',
}
# Quoted literals are consumed first, so metric names in legend/label_replace
# strings and label values are never rewritten. A selector consumes its entire
# label block, keeping grouping labels and matcher keys out of the scan.
TOKENS = re.compile(r'"(?:\\.|[^"\\])*"|`[^`]*`|\b(?P<metric>(?:tailscale2otel_|tailscale_|tailscaled_|process_)[a-zA-Z0-9_]+)(?P<labels>\{(?:"(?:\\.|[^"\\])*"|[^}"])*\})?')


def selectors(expr):
    # A family selector is a real vector selector even without a metric token.
    yield from re.finditer(r'(?P<metric>)(?P<labels>\{(?=[^}]*__name__=~"tailscale)[^}]*\})', expr)
    for match in TOKENS.finditer(expr):
        metric = match.group('metric')
        if metric and (metric in UNSUFFIXED or metric.startswith('tailscaled_') or
                       metric.endswith(('_total', '_ratio', '_seconds', '_bytes',
                                        '_days', '_bucket', '_sum', '_count'))):
            yield match


def metric_controls(metric, variables):
    """Only dimensions actually emitted by this family are applicable."""
    out = {}
    if 'tailnet' in variables and metric not in PROCESS_METRICS and not metric.startswith(PROCESS_PREFIXES):
        out['tailscale_tailnet'] = 'tailnet'
    if 'provider' in variables:
        out['tailscale2otel_provider'] = 'provider'
    if metric.startswith(('tailscale2otel_scrape_', 'tailscale2otel_subrequest_')) or metric in {
        'tailscale2otel_api_availability_ratio', 'tailscale2otel_api_last_probe_seconds',
        'tailscale2otel_capability_status_ratio', 'tailscale2otel_checkpoint_persist_errors_total',
        'tailscale2otel_log_catchup_skipped_seconds_total',
    }:
        if 'collector' in variables:
            out['tailscale_collector'] = 'collector'
    if metric.startswith('tailscale_device_') and not metric.startswith(('tailscale_device_invite', 'tailscale_device_attributes_dropped')):
        if 'host_name' in variables:
            out['host_name'] = 'host_name'
    os_metrics = {'tailscale_devices_count_ratio'} | {
        'tailscale_device_' + name for name in (
            'online_ratio', 'last_seen_seconds', 'key_expiry_seconds',
            'key_expiry_disabled_ratio', 'ssh_enabled_ratio',
            'blocks_incoming_connections_ratio', 'multiple_connections_ratio',
            'posture_identity_disabled_ratio', 'update_available_ratio', 'version_skew_ratio')}
    if 'os_type' in variables and metric in os_metrics:
        out['os_type'] = 'os_type'
    if 'os_type' in variables and metric == 'tailscale_device_posture_ratio':
        out['os'] = 'os_type'  # curated posture OS is named `os`, not `os_type`
    flow_dims = metric.startswith(('tailscale_network_io_', 'tailscale_network_packets_')) or metric == 'tailscale_network_flows_total'
    if flow_dims and 'net_transport' in variables:
        out['network_transport'] = 'net_transport'
    traffic_dims = flow_dims or metric in {'tailscale_network_field_observations_total', 'tailscale_network_dedup_conflicts_total'}
    if traffic_dims and 'traffic_type' in variables:
        out['tailscale_traffic_type'] = 'traffic_type'
    return out


def scope_promql(expr, variables):
    replacements = []
    for match in selectors(expr):
        labels = (match.group('labels') or '{}')[1:-1]
        additions = []
        for label, variable in metric_controls(match.group('metric'), variables).items():
            matcher = '%s=~"$%s"' % (label, variable)
            if matcher not in labels:
                additions.append(matcher)
        if additions:
            replacements.append((match.start(), match.end(), match.group('metric') + '{' + ', '.join(([labels] if labels else []) + additions) + '}'))
    for start, end, text in reversed(replacements):
        expr = expr[:start] + text + expr[end:]
    return expr


def scope_loki(expr, variables, raw_stream=False):
    # All domain log events are runtime signals. Config advisories are process-only.
    if 'tailscale2otel.config.advisory' in expr:
        return expr
    anchor = '{service_name="tailscale2otel"}'
    additions = []
    for label, variable in (('tailscale_tailnet', 'tailnet'), ('tailscale2otel_provider', 'provider')):
        if variable in variables and ('%s=~"$%s"' % (label, variable)) not in expr:
            additions.append('| %s=~"$%s"' % (label, variable))
    if 'tailscale.device.change' in expr:
        for label in ('host_name', 'os_type'):
            if label in variables and '$' + label not in expr:
                additions.append('| %s=~"$%s"' % (label, label))
    elif 'tailscale.device_invite' in expr and 'host_name' in variables and '$host_name' not in expr:
        additions.append('| host_name=~"$host_name"')
    if 'tailscale.network.flow' in expr:
        for label, variable in (('network_transport', 'net_transport'), ('tailscale_traffic_type', 'traffic_type')):
            if variable in variables and '$' + variable not in expr:
                additions.append('| %s=~"$%s"' % (label, variable))
    if additions:
        expr = expr.replace(anchor, anchor + ' ' + ' '.join(additions))
    if raw_stream and 'log_filter' in variables and '$log_filter' not in expr:
        expr += ' |~ `$log_filter`'
    # The generic event stream/volume obey the event selector; audit-specific
    # questions remain audit-specific, just as fixed-event streams on other tabs do.
    if 'log_event' in variables and 'event_name != ``' in expr:
        expr = expr.replace('event_name != ``', 'event_name=~`$log_event`')
    return expr


def scope_tempo(expr):
    # Tailnet ownership is already scoped by tempo_t. Only scheduler
    # `scrape <collector>` spans
    # declare tailscale.collector. Keep the original name predicate, and exempt
    # non-scheduler names from the collector condition (including mixed regexes).
    # A broad scrape-name scan also covers `scrape .+` and nodemetrics.scrape;
    # the negative-name branch makes unrelated classes behavior-preserving.
    if 'span.tailscale.collector =~ "${collector:regex}"' in expr:
        return expr

    def scoped_name(match):
        if 'scrape' not in match.group():
            return match.group()
        return ('(' + match.group() + ' && (name !~ "scrape .+" || '
                'span.tailscale.collector =~ "${collector:regex}"))')

    return re.sub(r'\bname\s*=~\s*"(?:\\.|[^"\\])*"', scoped_name, expr)


def scope_queries(elements, variables):
    names = {v['spec']['name'] for v in variables}
    for element in elements:
        # Fleet aggregates cannot acquire device filters on only one operand.
        # Retain ownership controls and consume metadata before schema output.
        population = element.pop('_population', 'selected')
        panel_names = names - {'os_type', 'host_name'} if population == 'fleet' else names
        ps = element['spec']
        for pq in ps['data']['spec']['queries']:
            query = pq['spec']['query']
            ds = query.get('datasource', {}).get('name')
            qs = query['spec']
            if ds == '${ds_prometheus}':
                qs['expr'] = scope_promql(qs['expr'], panel_names)
            elif ds == '${ds_loki}':
                qs['expr'] = scope_loki(qs['expr'], names, ps['vizConfig']['group'] == 'logs')
            elif ds == '${ds_tempo}' and 'collector' in names:
                qs['query'] = scope_tempo(qs['query'])
