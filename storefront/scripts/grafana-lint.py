#!/usr/bin/env python3
"""Checks a project's own Grafana dashboards (JSON) before they are imported into its org. No network, no Grafana needed.

  python3 scripts/grafana-lint.py deploy/grafana            # every *.json in the directory (files starting with _ are examples, skipped)
  python3 scripts/grafana-lint.py deploy/grafana/orders.json

Exit code 1 when something would break in the project's Grafana org (ERROR); WARN lines are advice.
What the org provides: data sources uid `prometheus` (the app's metrics), `prometheus-infra` (containers and uptime probes) and
`tempo` (the app's traces). Nothing else exists there, and every one of them is already filtered to the project.
"""
import json
import pathlib
import re
import sys

ALLOWED = {"prometheus": "prometheus", "prometheus-infra": "prometheus", "tempo": "tempo", "grafana": "datasource"}


def datasources(node, path="$"):
    """Every datasource reference below node: yields (path, value)."""
    if isinstance(node, dict):
        for k, v in node.items():
            if k == "datasource":
                yield f"{path}.datasource", v
            yield from datasources(v, f"{path}.{k}")
    elif isinstance(node, list):
        for i, v in enumerate(node):
            yield from datasources(v, f"{path}[{i}]")


def exprs(node, path="$"):
    if isinstance(node, dict):
        for k, v in node.items():
            if k in ("expr", "query") and isinstance(v, str):
                yield f"{path}.{k}", v
            else:
                yield from exprs(v, f"{path}.{k}")
    elif isinstance(node, list):
        for i, v in enumerate(node):
            yield from exprs(v, f"{path}[{i}]")


def dotted_metric(expr):
    """An OTel-style name (orders.created) used as a PromQL metric: Prometheus names use '_' and a _total/_seconds suffix."""
    s = re.sub(r'"(?:[^"\\]|\\.)*"', '""', expr)      # drop label values
    s = re.sub(r"\{[^}]*\}", "{}", s)                # drop matchers
    m = re.search(r"(?<![\w.$])([a-z][a-z0-9_]*(?:\.[a-z0-9_]+)+)(?=\s*[{\[(]|\s*$|\s*[)+\-*/,])", s)
    return m.group(1) if m else None


def lint(file):
    errs, warns = [], []
    try:
        d = json.loads(pathlib.Path(file).read_text())
    except ValueError as e:
        return [f"not valid JSON: {e}"], []
    if isinstance(d, dict) and "dashboard" in d and "panels" not in d:
        d = d["dashboard"]  # {"dashboard": {...}} as the HTTP API exports it
    if not isinstance(d, dict) or not (d.get("panels") or d.get("rows")):
        return ["not a Grafana dashboard (no panels)"], []
    if not d.get("title"):
        errs.append("missing title")
    raw = json.dumps(d)
    if "__inputs" in d or "${DS_" in raw or "DS_PROMETHEUS" in raw:
        errs.append('exported with "Export for sharing externally": it has __inputs / ${DS_...} placeholders. Re-export without that option, '
                    'or replace them with {"type": "prometheus", "uid": "prometheus"}')
    for path, ds in datasources(d):
        if ds is None:
            continue  # the org's default data source (prometheus)
        if isinstance(ds, str):
            errs.append(f'{path}: data source given as a string ("{ds}"). Use {{"type": "prometheus", "uid": "prometheus"}} '
                        f"(uids available: {', '.join(sorted(k for k in ALLOWED if k != 'grafana'))})")
            continue
        uid, typ = ds.get("uid"), ds.get("type")
        if uid not in ALLOWED:
            errs.append(f"{path}: data source uid {uid!r} does not exist in a project org (only: {', '.join(sorted(k for k in ALLOWED if k != 'grafana'))})")
        elif typ and typ != ALLOWED[uid]:
            errs.append(f"{path}: uid {uid!r} is a {ALLOWED[uid]} data source, not {typ!r}")
    for path, e in exprs(d):
        name = dotted_metric(e)
        if name:
            warns.append(f"{path}: '{name}' looks like an OpenTelemetry name; in Prometheus it is '{name.replace('.', '_')}' "
                         "(+ _total for counters, + _seconds/_bytes/_milliseconds for the unit; histograms add _bucket/_sum/_count)")
        if "job=" in e and re.search(r'job\s*=~?\s*"[^"/]*"', e) and not re.search(r'job\s*=~?\s*"[^"]*/', e):
            warns.append(f"{path}: job label values look like '<project>/<service>'; a filter without the slash matches nothing. "
                         "You do not need a project filter at all: the data source already returns only this project.")
    if d.get("uid"):
        warns.append(f"uid {d['uid']!r} is replaced on import (p-<project>-c-<file name>); do not link to it from other dashboards")
    if not d.get("schemaVersion"):
        warns.append("no schemaVersion (export the dashboard from Grafana instead of writing it by hand, or copy deploy/grafana/_example.json)")
    return errs, warns


def main(argv):
    if not argv:
        print(__doc__)
        return 2
    files = []
    for a in argv:
        p = pathlib.Path(a)
        files += sorted(f for f in p.glob("*.json") if not f.name.startswith("_")) if p.is_dir() else [p]
    if not files:
        print("no dashboards to check (files starting with _ are skipped)")
        return 0
    bad = 0
    for f in files:
        errs, warns = lint(f)
        for e in errs:
            print(f"ERROR {f}: {e}")
        for w in warns:
            print(f"WARN  {f}: {w}")
        bad += bool(errs)
        if not errs and not warns:
            print(f"ok    {f}")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
