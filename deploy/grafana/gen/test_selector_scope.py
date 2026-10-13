"""Every emitted panel query, every tab, and every applicable control.

Expectations for local dimensions come independently from the catalog-generated
metric reference. A missing matcher in ANY operand fails, not just a missing
variable anywhere in a long ratio expression. Mutations happen after assembly,
so the test observes the document Grafana consumes, not the scoping helper.
"""
import copy
import json
from pathlib import Path
import re
import unittest

import build
import dashboards
from selector_scope import selectors
from test_query_budget import _collect_leaf_tabs, _refs

ROOT = Path(__file__).resolve().parents[3]


def catalog_labels():
    out = {}
    for line in (ROOT / 'docs/metrics.md').read_text().splitlines():
        cols = re.split(r'(?<!\\)\|', line)[1:-1]
        if len(cols) != 6 or not cols[0].strip().startswith('`'):
            continue
        metric = cols[3].strip().strip('`')
        attrs = set(re.findall(r'`([^`]+)`', cols[4]))
        for suffix in ('', '_bucket', '_sum', '_count'):
            out[metric + suffix] = attrs
    return out


LABELS = catalog_labels()
# Independent ownership expectations, not the production exemption oracle.
# Shared reporters/services use procEmitter (app.New/Run); checkpoint stats use
# RunCheckpointReporter(a.procEmitter). Persist errors instead come from runtime
# schedulers/processors (tailnetruntime.go, scheduler.go, audit/processor.go).
# Deliberately name the two file stats exactly: a new checkpoint signal must not
# inherit process ownership merely by sharing their prefix.
PROCESS_OWNED_FAMILIES = (
    'process_', 'tailscale_rdns_', 'tailscale_geoip_',
    'tailscale_organization_', 'tailscale2otel_admin_', 'tailscale2otel_annotation_',
    'tailscale2otel_build_', 'tailscale2otel_capability_',
    'tailscale2otel_component_', 'tailscale2otel_config_',
    'tailscale2otel_coordination_', 'tailscale2otel_ingress_wal_',
    'tailscale2otel_metrics_', 'tailscale2otel_pii_', 'tailscale2otel_profiling_',
    'tailscale2otel_export_failures_', 'tailscale2otel_receiver_',
    'tailscale2otel_runtime_', 'tailscale2otel_tls_',
    'tailscale2otel_up_', 'tailscale2otel_update_',
)
PROCESS_OWNED_CHECKPOINT_STATS = {
    'tailscale2otel_checkpoint_disk_size_bytes',
    'tailscale2otel_checkpoint_persist_age_seconds',
}


def expected_process_owned(metric):
    return metric in PROCESS_OWNED_CHECKPOINT_STATS or metric.startswith(PROCESS_OWNED_FAMILIES)


LOCAL = {'collector': 'tailscale_collector', 'os_type': 'os_type',
         'host_name': 'host_name', 'net_transport': 'network_transport',
         'traffic_type': 'tailscale_traffic_type'}


def trace_accepts(expr, name, collector=None, selected='devices', tailnet=None, selected_tailnet='.*'):
    """Evaluate the emitted span predicate, NOT the production rewrite trigger.

    This bounded evaluator covers the string comparisons/Boolean operators used
    by these dashboards. It deliberately fails on unsupported syntax rather than
    silently treating an unparsed query as scoped. No Tempo execution is claimed.
    """
    # Mutually exclusive All/selected span sets are joined at the span-set
    # level, rather than requiring a missing attribute in a Boolean filter.
    selectors = list(re.finditer(r'\{((?:"(?:\\.|[^"\\])*"|`[^`]*`|[^"`}])*)\}', expr))
    if len(selectors) > 1:
        if len(selectors) != 2 or expr[selectors[0].end():selectors[1].start()].strip() != '||':
            raise AssertionError('Unsupported TraceQL span-set combination: ' + expr)
        return any(trace_accepts('{' + stage.group(1) + '}', name, collector,
                                 selected, tailnet, selected_tailnet)
                   for stage in selectors)
    selector = re.match(r'\{((?:"(?:\\.|[^"\\])*"|`[^`]*`|[^"`}])*)\}', expr)
    if not selector:
        raise AssertionError('Unsupported TraceQL selector: ' + expr)
    text = selector.group(1)
    token_pattern = re.compile(r'\s*("(?:\\.|[^"\\])*"|`[^`]*`|&&|\|\||=~|!~|!=|=|\(|\)|[\w.]+)')
    tokens = []
    pos = 0
    while text[pos:].strip():
        match = token_pattern.match(text, pos)
        if not match:
            raise AssertionError('Unsupported TraceQL token: ' + text[pos:])
        tokens.append(match.group(1))
        pos = match.end()
    values = {'name': name, 'resource.service.name': 'tailscale2otel',
              'span.tailscale.collector': collector or '',
              'span.tailscale.tailnet': tailnet,
              'span.tailscale.endpoint': '/devices' if name.startswith(('tailscale.api ', 'headscale.api ')) else ''}
    index = 0

    def atom():
        nonlocal index
        if tokens[index] == '(':
            index += 1
            result = disjunction()
            if tokens[index] != ')':
                raise AssertionError('Unbalanced TraceQL predicate: ' + expr)
            index += 1
            return result
        field, op, literal = tokens[index:index + 3]
        index += 3
        def resolve(token):
            if token.startswith(('"', '`')):
                value = token[1:-1] if token.startswith('`') else json.loads(token)
                return value.replace('${collector:regex}', selected).replace('${tailnet:regex}', selected_tailnet)
            if token not in values:
                raise AssertionError('Unsupported TraceQL comparison: ' + expr)
            return values[token]
        value, expected = resolve(field), resolve(literal)
        if op in ('=~', '!~'):
            matches = value is not None and re.fullmatch(expected, value) is not None
            return matches if op == '=~' else not matches
        if op in ('=', '!='):
            return (value == expected) if op == '=' else (value != expected)
        raise AssertionError('Unsupported TraceQL operator: ' + op)

    def conjunction():
        nonlocal index
        result = atom()
        while index < len(tokens) and tokens[index] == '&&':
            index += 1
            right = atom()
            result = result and right
        return result

    def disjunction():
        nonlocal index
        result = conjunction()
        while index < len(tokens) and tokens[index] == '||':
            index += 1
            right = conjunction()
            result = result or right
        return result

    result = disjunction()
    if index != len(tokens):
        raise AssertionError('Unparsed TraceQL predicate: ' + expr)
    return result


def violations(doc):
    offenders = []
    globals_ = doc['spec']['variables']
    for tab in _collect_leaf_tabs(doc):
        variables = globals_ + tab['spec'].get('variables', [])
        names = {v['spec']['name'] for v in variables}
        title = tab['spec']['title']
        for ref in _refs(tab['spec']['layout']):
            panel = doc['spec']['elements'][ref]['spec']
            for pq in panel['data']['spec']['queries']:
                query = pq['spec']['query']
                ds = query.get('datasource', {}).get('name')
                expr = query['spec'].get('expr', query['spec'].get('query', ''))
                identity = (doc['metadata']['name'], title, panel['title'], pq['spec']['refId'])
                if ds == '${ds_prometheus}':
                    for match in selectors(expr):
                        metric = match.group('metric')
                        attrs = LABELS.get(metric, set())
                        expected = {}
                        if 'tailnet' in names and not expected_process_owned(metric):
                            expected['tailscale_tailnet'] = 'tailnet'
                        if 'provider' in names:
                            expected['tailscale2otel_provider'] = 'provider'
                        for var, label in LOCAL.items():
                            if var in names and label in attrs:
                                expected[label] = var
                        if metric == 'tailscale_device_posture_ratio' and 'os_type' in names:
                            expected['os'] = 'os_type'
                        labels = match.group('labels') or ''
                        for label, var in expected.items():
                            if '%s=~"$%s"' % (label, var) not in labels:
                                offenders.append((identity, metric, var, labels))
                elif ds == '${ds_loki}' and 'tailscale2otel.config.advisory' not in expr:
                    for label, var in (('tailscale_tailnet', 'tailnet'), ('tailscale2otel_provider', 'provider')):
                        if var in names and '%s=~"$%s"' % (label, var) not in expr:
                            offenders.append((identity, ds, var, expr))
                    if panel['vizConfig']['group'] == 'logs' and 'log_filter' in names and '$log_filter' not in expr:
                        offenders.append((identity, ds, 'log_filter', expr))
                    if 'tailscale.network.flow' in expr:
                        for var in ('net_transport', 'traffic_type'):
                            if var in names and '$' + var not in expr:
                                offenders.append((identity, ds, var, expr))
                    if 'tailscale.device.change' in expr:
                        for var in ('host_name', 'os_type'):
                            if var in names and '$' + var not in expr:
                                offenders.append((identity, ds, var, expr))
                    if 'tailscale.device_invite' in expr and 'host_name' in names and '$host_name' not in expr:
                        offenders.append((identity, ds, 'host_name', expr))
                    if panel['title'] == 'Log volume by event' and '$log_event' not in expr:
                        offenders.append((identity, ds, 'log_event', expr))
                elif ds == '${ds_tempo}':
                    # Runtime spans carry ownership; All must still retain
                    # process spans without manufacturing a tailnet dimension.
                    if 'span.tailscale.tailnet' not in expr or '${tailnet:regex}' not in expr:
                        offenders.append((identity, ds, 'tailnet', expr))
                    if 'collector' in names:
                        # Probe actual scheduler span acceptance, including mixed
                        # class selectors. A variable elsewhere in the expression
                        # is not enough: non-selected scheduler spans must fail.
                        for collector in ('devices', 'users'):
                            other = 'users' if collector == 'devices' else 'devices'
                            if trace_accepts(expr, 'scrape ' + collector, collector, other):
                                offenders.append((identity, ds, 'collector', expr))
                                break
    return offenders


class SelectorScopeTest(unittest.TestCase):
    def test_every_dashboard_and_tab(self):
        for spec in dashboards.ALL:
            doc = build.build(spec)
            with self.subTest(dashboard=spec.uid):
                self.assertEqual(violations(doc), [])

    def test_shipped_documents_are_scoped_too(self):
        for spec in dashboards.ALL:
            doc = json.loads((ROOT / spec.out).read_text())
            self.assertEqual(violations(doc), [], spec.uid)

    def mixed_trace_query(self, doc):
        panels = [el['spec'] for el in doc['spec']['elements'].values()
                  if el['spec']['title'] == 'Span class activity']
        self.assertEqual(len(panels), 1)
        queries = panels[0]['data']['spec']['queries']
        self.assertEqual(len(queries), 1)
        query = queries[0]['spec']['query']
        self.assertEqual(query['datasource']['name'], '${ds_tempo}')
        return query['spec']

    def test_mixed_scheduler_scope_preserves_other_span_classes(self):
        for surface, doc in self.checkpoint_documents():
            with self.subTest(surface=surface):
                expr = self.mixed_trace_query(doc)['query']
                self.assertTrue(expr.endswith(' | rate() by (name)'), expr)
                for collector in ('devices', 'users'):
                    self.assertTrue(trace_accepts(expr, 'scrape ' + collector, collector, collector), expr)
                    other = 'users' if collector == 'devices' else 'devices'
                    self.assertFalse(trace_accepts(expr, 'scrape ' + collector, collector, other), expr)
                    self.assertTrue(trace_accepts(expr, 'scrape ' + collector, collector, '.*'), expr)
                # These span classes have NO collector attribute.
                for name in ('tailscale.api GET /devices', 'headscale.api GET /node',
                             'nodemetrics.scrape', 'stream.receive', 'webhook.receive',
                             'release.check latest'):
                    for selected in ('devices', 'users', '.*'):
                        self.assertTrue(trace_accepts(expr, name, selected=selected), (name, selected, expr))
                self.assertFalse(trace_accepts(expr, 'unrelated.span'), expr)
                self.assertIn('span.tailscale.tailnet', expr)

    def test_trace_ownership_keeps_all_and_isolates_selected_runtime(self):
        for surface, doc in self.checkpoint_documents():
            with self.subTest(surface=surface):
                expr = self.mixed_trace_query(doc)['query']
                for name in ('scrape devices', 'tailscale.api GET /devices',
                             'nodemetrics.scrape', 'stream.receive', 'webhook.receive'):
                    for owner in ('alpha.example', 'beta.example'):
                        collector = 'devices' if name.startswith('scrape ') else None
                        self.assertTrue(trace_accepts(expr, name, collector, '.*', owner, '.*'))
                        self.assertTrue(trace_accepts(expr, name, collector, '.*', owner, owner))
                        other = 'beta.example' if owner == 'alpha.example' else 'alpha.example'
                        self.assertFalse(trace_accepts(expr, name, collector, '.*', owner, other))
                self.assertTrue(trace_accepts(expr, 'release.check latest', selected='.*'))
                self.assertFalse(trace_accepts(expr, 'release.check latest', selected='.*', selected_tailnet='alpha.example'))
                self.assertFalse(trace_accepts(expr, 'stream.receive', selected='.*', selected_tailnet='alpha.example'))

    def test_guard_rejects_unscoped_mixed_scheduler_on_both_surfaces(self):
        for surface, doc in self.checkpoint_documents():
            with self.subTest(surface=surface):
                query = self.mixed_trace_query(doc)
                # Replace just the real collector predicate with a tautology;
                # keep a valid selector and all original name alternatives.
                query['query'], changed = re.subn(
                    r'span\.tailscale\.collector =~ "\$\{collector:regex\}"',
                    'name =~ ".*"', query['query'])
                self.assertEqual(changed, 2, query['query'])
                found = violations(doc)
                self.assertTrue(any(x[0][2] == 'Span class activity' and
                                    x[2] == 'collector' for x in found), found)

    def test_mixed_trace_proof_rejects_blanket_filter_and_ineffective_scope(self):
        for surface, doc in self.checkpoint_documents():
            with self.subTest(surface=surface):
                expr = self.mixed_trace_query(doc)['query']
                # A blanket collector condition would erase collector-less API
                # spans. Prove the behavioral check distinguishes that repair.
                blanket, changed = re.subn(r'name !~ "scrape \.\+" \|\| ', '', expr)
                self.assertEqual(changed, 2, expr)
                self.assertFalse(trace_accepts(blanket, 'tailscale.api GET /devices'))
                # Merely retaining the variable reference is insufficient: an
                # inverted exemption lets every scheduler through despite it.
                query = self.mixed_trace_query(doc)
                query['query'], changed = re.subn(r'name !~ "scrape \.\+"',
                                                 'name =~ "scrape .+"', expr)
                self.assertEqual(changed, 2, expr)
                self.assertIn('${collector:regex}', query['query'])
                self.assertTrue(any(x[0][2] == 'Span class activity' and
                                    x[2] == 'collector' for x in violations(doc)))

    def test_inventory_observes_every_panel_and_selector(self):
        panels = metrics = tabs = 0
        for spec in dashboards.ALL:
            doc = build.build(spec)
            refs = [ref for tab in _collect_leaf_tabs(doc) for ref in _refs(tab['spec']['layout'])]
            self.assertEqual(set(refs), set(doc['spec']['elements']))
            self.assertEqual(len(refs), len(set(refs)))
            panels += len(refs)
            tabs += len(_collect_leaf_tabs(doc))
            for el in doc['spec']['elements'].values():
                for q in el['spec']['data']['spec']['queries']:
                    qs = q['spec']['query']
                    if qs.get('datasource', {}).get('name') == '${ds_prometheus}':
                        found = list(selectors(qs['spec']['expr']))
                        self.assertTrue(found, el['spec']['title'])
                        metrics += len(found)
        self.assertGreaterEqual(tabs, 22)
        self.assertGreater(panels, 500)
        self.assertGreater(metrics, panels)  # every ratio/fallback operand is counted
        self.assertGreater(len(LABELS), 300)

    def test_all_catalog_metric_tokens_are_observed(self):
        # An unknown suffix must not make a new metric invisible to the guard.
        for spec in dashboards.ALL:
            doc = build.build(spec)
            for el in doc['spec']['elements'].values():
                for pq in el['spec']['data']['spec']['queries']:
                    q = pq['spec']['query']
                    if q['datasource']['name'] != '${ds_prometheus}':
                        continue
                    expr = q['spec']['expr']
                    plain = re.sub(r'"(?:\\.|[^"\\])*"', '""', expr)
                    expected = {token for token in re.findall(r'\b[a-zA-Z_][a-zA-Z_0-9]*\b', plain) if token in LABELS}
                    observed = {m.group('metric') for m in selectors(expr)}
                    self.assertTrue(expected <= observed, (el['spec']['title'], expected - observed))

    def test_query_variables_scope_options_and_presence_without_cycles(self):
        for spec in dashboards.ALL:
            doc = build.build(spec)
            for tab in _collect_leaf_tabs(doc):
                variables = doc['spec']['variables'] + tab['spec'].get('variables', [])
                names = {v['spec']['name'] for v in variables}
                deps = {}
                for v in variables:
                    vs = v['spec']
                    query = vs.get('query')
                    if not isinstance(query, dict):
                        continue
                    expr = query['spec']['query']
                    deps[vs['name']] = set(re.findall(r'\$([a-zA-Z_][a-zA-Z_0-9]*)', expr)) & names
                    if vs['name'] in {'tailnet', 'provider'}:
                        continue  # independently enumerate the selectable universe
                    # Dashboard variables cannot reference a tab-local provider;
                    # only variables declared on that tab can see it.
                    visible = {g['spec']['name'] for g in doc['spec']['variables']} if v in doc['spec']['variables'] else names
                    for m in selectors(expr):
                        labels = m.group('labels') or ''
                        if not expected_process_owned(m.group('metric')):
                            self.assertIn('tailscale_tailnet=~"$tailnet"', labels, (tab['spec']['title'], vs['name']))
                        if 'provider' in visible:
                            self.assertIn('tailscale2otel_provider=~"$provider"', labels, vs['name'])
                def visit(name, stack):
                    self.assertNotIn(name, stack, (spec.uid, tab['spec']['title'], stack, name))
                    for dep in deps.get(name, ()):
                        visit(dep, stack + [name])
                for name in deps:
                    visit(name, [])

    def checkpoint_documents(self):
        # Both the assembly path and the actual generated Grafana document.
        yield 'built', build.build(dashboards.HEALTH)
        yield 'shipped', json.loads((ROOT / dashboards.HEALTH.out).read_text())

    def checkpoint_query(self, doc, title, metric):
        panels = [el['spec'] for el in doc['spec']['elements'].values()
                  if el['spec']['title'] == title]
        self.assertEqual(len(panels), 1, title)
        queries = [pq['spec']['query']['spec']
                   for pq in panels[0]['data']['spec']['queries']
                   if metric in pq['spec']['query']['spec'].get('expr', '')]
        self.assertEqual(len(queries), 1, title)
        return queries[0]

    def test_checkpoint_runtime_errors_are_scoped(self):
        # Direct assertion on the rendered operand, independent even of selectors()
        # and the guard's ownership expectations. Fail on the retained candidate.
        metric = 'tailscale2otel_checkpoint_persist_errors_total'
        for surface, doc in self.checkpoint_documents():
            with self.subTest(surface=surface):
                query = self.checkpoint_query(doc, 'Checkpoint persist errors/s', metric)
                operands = re.findall(r'\b' + metric + r'(\{[^}]*\})?', query['expr'])
                self.assertTrue(operands, query['expr'])
                for labels in operands:
                    self.assertIn('tailscale_tailnet=~"$tailnet"', labels, query['expr'])
                    self.assertIn('tailscale_collector=~"$collector"', labels, query['expr'])

    def test_guard_rejects_unscoped_rendered_checkpoint_errors(self):
        metric = 'tailscale2otel_checkpoint_persist_errors_total'
        for surface, doc in self.checkpoint_documents():
            with self.subTest(surface=surface):
                query = self.checkpoint_query(doc, 'Checkpoint persist errors/s', metric)
                query['expr'], changed = re.subn(
                    r'tailscale_tailnet=~"\$tailnet"\s*,?\s*', '', query['expr'], count=1)
                self.assertEqual(changed, 1, query['expr'])
                found = violations(doc)
                self.assertTrue(any(x[0][2] == 'Checkpoint persist errors/s' and
                                    x[1] == metric and x[2] == 'tailnet' for x in found), found)

    def test_checkpoint_file_stats_remain_process_scoped(self):
        # Only these two checkpoint metrics use the process emitter. Do not use
        # the production exception list to decide what the rendered query needs.
        for surface, doc in self.checkpoint_documents():
            for title, metric in (
                ('Checkpoint disk', 'tailscale2otel_checkpoint_disk_size_bytes'),
                ('Checkpoint persist age', 'tailscale2otel_checkpoint_persist_age_seconds'),
            ):
                with self.subTest(surface=surface, panel=title):
                    query = self.checkpoint_query(doc, title, metric)
                    self.assertNotIn('tailscale_tailnet', query['expr'])

    def test_guard_rejects_unscoped_real_ratio_operand(self):
        doc = copy.deepcopy(build.build(dashboards.TAILNET))
        p = next(el['spec'] for el in doc['spec']['elements'].values() if el['spec']['title'] == 'Targets up / total')
        qs = p['data']['spec']['queries'][0]['spec']['query']['spec']
        qs['expr'], changed = re.subn(r'(tailscale_node_up_ratio)\{[^}]*\}', r'\1', qs['expr'], count=1)
        self.assertEqual(changed, 1)
        found = violations(doc)
        self.assertTrue(any(x[0][2] == 'Targets up / total' and x[2] == 'tailnet' for x in found), found)

    def test_guard_rejects_each_local_control_removed_from_rendered_query(self):
        for var in ('collector', 'os_type', 'host_name', 'net_transport', 'traffic_type'):
            changed = False
            for spec in dashboards.ALL:
                doc = build.build(spec)
                for el in doc['spec']['elements'].values():
                    for q in el['spec']['data']['spec']['queries']:
                        qs = q['spec']['query']
                        if qs.get('datasource', {}).get('name') != '${ds_prometheus}':
                            continue
                        expr = qs['spec']['expr']
                        if '=~"$%s"' % var in expr:
                            qs['spec']['expr'] = re.sub(r'\w+=~"\$' + var + r'"\s*,?\s*', '', expr)
                            self.assertTrue(any(x[2] == var for x in violations(doc)), var)
                            changed = True
                            break
                    if changed:
                        break
                if changed:
                    break
            self.assertTrue(changed, var)

    def test_ad_hoc_controls_bind_the_queried_datasource(self):
        for spec in dashboards.ALL:
            doc = build.build(spec)
            for tab in _collect_leaf_tabs(doc):
                variables = doc['spec']['variables'] + tab['spec'].get('variables', [])
                for v in variables:
                    if v['kind'] == 'AdhocVariable':
                        self.assertEqual(v['group'], 'prometheus')
                        self.assertEqual(v['datasource']['name'], '${ds_prometheus}')
                        self.assertTrue(v['spec']['baseFilters'])
                        self.assertTrue(any(q['spec']['query']['datasource']['name'] == '${ds_prometheus}'
                                            for ref in _refs(tab['spec']['layout'])
                                            for q in doc['spec']['elements'][ref]['spec']['data']['spec']['queries']))


if __name__ == '__main__':
    unittest.main()
