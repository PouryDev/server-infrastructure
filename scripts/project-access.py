#!/usr/bin/env python3
"""Gives every project its own Grafana ORG whose metrics are filtered at the source (isolation you can rely on).

  scripts/project-access.py gapchi reels-chi            # org + data sources + dashboards for these projects
  scripts/project-access.py --discover                  # ... for every dir in config/grafana/projects/
  scripts/project-access.py gapchi --user m.ahmadi      # also a login that belongs to that org ONLY
  scripts/project-access.py gapchi --user m.ahmadi --reset-password
  scripts/project-access.py gapchi --remove             # delete the project's org (and its dashboards)

Per project <p> (Grafana HTTP API as the admin from .env, http://127.0.0.1:$GRAFANA_PORT):
  * org "<p>", with exactly two Prometheus data sources, both pointing at a label proxy (compose services
    label-proxy-job / label-proxy-project) with a secret header that fixes the tenant:
      uid prometheus        -> every query gets job=~"<p>/.*"   (the app's OpenTelemetry metrics)
      uid prometheus-infra  -> every query gets project="<p>"   (its containers and uptime probes)
    The proxy ANDs that matcher into every query AND every label/series API, so other projects' metrics are not
    readable even through a hand-written query in Explore. A project cannot change the data sources (needs org Admin).
  * the project's dashboards (config/grafana/projects/<p>/*.json, from scripts/project-dashboards.py) pushed into it.
    The generated ones (tag "generated") are overwritten on every run: use "Save as" to keep your own variant.
    Dashboards you create in the org are never touched.
  * --user: a login with role Editor in that org and in no other (not in the main org). Editors build dashboards and
    use Explore, within the filtered data sources. A password is generated and printed once (never stored).
Grafana server admins (the .env admin) still see everything, in every org.
Traces: Tempo runs multi-tenant. The collector routes every span to the Tempo tenant named like its service.namespace
(config/otel/tenants.yaml, written here; the collector is restarted when it changes), and the org's `tempo` data source sends that tenant
as X-Scope-OrgID, so a project org only ever sees its own traces. Spans of a project without a route land in tenant "single-tenant".
The admin org gets `tempo-all` (all tenants at once) and a "Projects" dashboard linking every project's org.
NOT covered: logs (no log backend).
"""
import argparse
import base64
import http.client
import json
import pathlib
import re
import secrets
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parent.parent
PROJECTS = ROOT / "config/grafana/projects"
sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
from dashlib import NAME_RE  # noqa: E402

PROXIES = {  # uid -> (name, proxy service, header value for project p)
    "prometheus": ("Prometheus", "label-proxy-job", lambda p: f"{p}/.*"),
    "prometheus-infra": ("Prometheus (containers & uptime)", "label-proxy-project", lambda p: p),
}


def env():
    e = {}
    f = ROOT / ".env"
    if f.exists():
        for line in f.read_text().splitlines():
            m = re.match(r"^([A-Z_]+)=(.*)$", line)
            if m:
                e[m.group(1)] = m.group(2).strip().strip('"')
    return e


class Grafana:
    def __init__(self):
        e = env()
        if not e.get("GRAFANA_ADMIN_PASSWORD"):
            sys.exit("GRAFANA_ADMIN_PASSWORD missing in .env")
        self.base = f"http://127.0.0.1:{e.get('GRAFANA_PORT', '3000')}"
        tok = base64.b64encode(f"{e.get('GRAFANA_ADMIN_USER', 'admin')}:{e['GRAFANA_ADMIN_PASSWORD']}".encode()).decode()
        self.h = {"Authorization": f"Basic {tok}", "Content-Type": "application/json"}

    def call(self, method, path, body=None, ok=(), org=None):
        """ok: HTTP codes that return {} instead of aborting. org: act inside that org id."""
        data = json.dumps(body).encode() if body is not None else None
        headers = dict(self.h, **({"X-Grafana-Org-Id": str(org)} if org else {}))
        deadline = time.time() + 90  # Grafana may still be starting (restart, first boot)
        while True:
            req = urllib.request.Request(self.base + path, method=method, headers=headers, data=data)
            try:
                with urllib.request.urlopen(req, timeout=30) as r:
                    raw = r.read()
                    return json.loads(raw) if raw else {}
            except urllib.error.HTTPError as e:
                if e.code in ok:
                    return {}
                sys.exit(f"grafana {method} {path}: HTTP {e.code} {e.read().decode()[:300]}")
            except (urllib.error.URLError, http.client.HTTPException, ConnectionError) as e:
                if time.time() > deadline:
                    sys.exit(f"cannot reach Grafana at {self.base}: {e}")
                time.sleep(3)


def ensure_org(g, name):
    o = g.call("GET", "/api/orgs/name/" + urllib.parse.quote(name), ok=(404,))
    return o["id"] if o else g.call("POST", "/api/orgs", {"name": name})["orgId"]


def tempo_body(uid, name, tenant):
    return {"uid": uid, "name": name, "type": "tempo", "access": "proxy", "url": "http://tempo:3200",
            "jsonData": {"httpHeaderName1": "X-Scope-OrgID", "nodeGraph": {"enabled": True}},
            "secureJsonData": {"httpHeaderValue1": tenant}}


def upsert_datasource(g, org, body, have):
    if body["uid"] in have:
        g.call("PUT", f"/api/datasources/uid/{body['uid']}", body, org=org)
    else:
        g.call("POST", "/api/datasources", body, org=org)


def ensure_datasources(g, org, project):
    have = {d["uid"]: d for d in g.call("GET", "/api/datasources", org=org)}
    for uid, (name, svc, tenant) in PROXIES.items():
        body = {"uid": uid, "name": name, "type": "prometheus", "access": "proxy", "url": f"http://{svc}:9091",
                "isDefault": uid == "prometheus",
                "jsonData": {"timeInterval": "30s", "httpHeaderName1": "X-Tenant"},
                "secureJsonData": {"httpHeaderValue1": tenant(project)}}
        if uid in have:
            g.call("PUT", f"/api/datasources/uid/{uid}", body, org=org)
        else:
            g.call("POST", "/api/datasources", body, org=org)
    upsert_datasource(g, org, tempo_body("tempo", "Tempo", project), have)
    for uid, d in have.items():  # nothing but the filtered sources may exist in a project org
        if uid not in PROXIES and uid != "tempo":
            g.call("DELETE", f"/api/datasources/uid/{uid}", org=org)


def push_dashboards(g, org, project):
    d = PROJECTS / project
    files = sorted(d.glob("*.json")) if d.is_dir() else []
    if not files:
        print(f"{project}: no dashboards in {d} (run scripts/project-dashboards.py {project})", file=sys.stderr)
    for f in files:
        dash = json.loads(f.read_text())
        dash["id"] = None
        g.call("POST", "/api/dashboards/db", {"dashboard": dash, "overwrite": True, "message": "generated by project-access.py"}, org=org)
    # imported dashboards (uid p-<project>-c-*) whose file is gone: remove them. Dashboards built in the UI have other uids, never touched.
    keep = {json.loads(f.read_text()).get("uid") for f in files}
    for h in g.call("GET", f"/api/search?type=dash-db&limit=5000&query=", org=org):
        if h["uid"].startswith(f"p-{project}-c-") and h["uid"] not in keep:
            g.call("DELETE", f"/api/dashboards/uid/{h['uid']}", org=org)
            print(f"{project}: removed dashboard {h['uid']} (its file is gone)")
    if (d / "overview.json").exists():
        g.call("PUT", "/api/org/preferences", {"homeDashboardUID": f"p-{project}-overview"}, org=org)
    return len(files)


def ensure_user(g, login, org, reset):
    u = g.call("GET", "/api/users/lookup?loginOrEmail=" + urllib.parse.quote(login), ok=(404,))
    pw = None
    if not u:
        pw = secrets.token_urlsafe(14)
        uid = g.call("POST", "/api/admin/users", {"name": login, "login": login, "email": f"{login}@project.local", "password": pw, "OrgId": org})["id"]
    else:
        uid = u["id"]
        if reset:
            pw = secrets.token_urlsafe(14)
            g.call("PUT", f"/api/admin/users/{uid}/password", {"password": pw})
    # member of this org only: add (or fix the role), then remove from every other org
    g.call("POST", f"/api/orgs/{org}/users", {"loginOrEmail": login, "role": "Editor"}, ok=(409,))
    g.call("PATCH", f"/api/orgs/{org}/users/{uid}", {"role": "Editor"})
    for o in g.call("GET", f"/api/users/{uid}/orgs"):
        if o["orgId"] != org:
            g.call("DELETE", f"/api/orgs/{o['orgId']}/users/{uid}", ok=(404,))
    return pw


def known_projects(extra):
    return sorted(set(extra) | {p.name for p in PROJECTS.glob("*") if p.is_dir()})


def sync_tenants(names):
    """One Tempo tenant per project: routing table + exporter + pipeline in config/otel/tenants.yaml. Restarts the collector on change."""
    lines = ["# GENERATED by scripts/project-access.py (make project-dashboards): one Tempo tenant per project. Do not edit.",
             "connectors:", "  routing/tenant:", "    default_pipelines: [traces/tenant-default]", "    table:",
             '      - statement: route() where attributes["service.namespace"] == "-no-project-"',
             "        pipelines: [traces/tenant-default]"]
    for n in names:
        lines += [f'      - statement: route() where attributes["service.namespace"] == "{n}"', f"        pipelines: [traces/tenant-{n}]"]
    if names:
        lines += ["exporters:"]
        for n in names:
            lines += [f"  otlp/tempo-{n}:", "    endpoint: tempo:4317", "    tls: { insecure: true }", f"    headers: {{ X-Scope-OrgID: {n} }}"]
        lines += ["service:", "  pipelines:"]
        for n in names:
            lines += [f"    traces/tenant-{n}:", "      receivers: [routing/tenant]", f"      exporters: [otlp/tempo-{n}]"]
    text = "\n".join(lines) + "\n"
    f = ROOT / "config/otel/tenants.yaml"
    if f.exists() and f.read_text() == text:
        return False
    f.write_text(text)
    r = subprocess.run(["docker", "compose", "restart", "otel-collector"], cwd=ROOT, capture_output=True, text=True)
    print("otel-collector: tenants changed, " + ("restarted" if r.returncode == 0 else f"RESTART FAILED, do it yourself: docker compose restart otel-collector ({r.stderr.strip()[:100]})"))
    return True


def hub(g, names):
    """Main org: the "Projects" dashboard (home) with a link per project org, and the all-tenants Tempo data source."""
    have = {d["uid"] for d in g.call("GET", "/api/datasources", org=1)}
    upsert_datasource(g, 1, tempo_body("tempo-all", "Tempo (all projects)", "|".join(["single-tenant"] + names)), have)
    rows = ["| Project | Overview | HTTP | Traces & latency | Database | Containers |", "|---|---|---|---|---|---|"]
    for n in names:
        o = g.call("GET", "/api/orgs/name/" + urllib.parse.quote(n), ok=(404,))
        if not o:
            continue
        link = lambda kind, label: f"[{label}](/d/p-{n}-{kind}?orgId={o['id']})"
        rows.append(f"| **{n}** | {link('overview', 'open')} | {link('http', 'HTTP')} | {link('traces', 'traces')} | {link('database', 'DB')} | {link('containers', 'containers')} |")
    md = ("Every project has its **own Grafana org** (its data is filtered to the project). The links switch org for you. "
          "Without a link: profile menu (bottom left) > the org switcher. Add a project: `make project-dashboards PROJECTS=name`.\n\n" + "\n".join(rows))
    dash = {"uid": "infra-projects", "title": "Projects", "tags": ["infrastructure"], "schemaVersion": 39, "editable": False, "refresh": "",
            "panels": [{"id": 1, "type": "text", "title": "", "gridPos": {"x": 0, "y": 0, "w": 24, "h": 4 + 2 * len(names)}, "options": {"mode": "markdown", "content": md}}],
            "id": None}
    g.call("POST", "/api/dashboards/db", {"dashboard": dash, "overwrite": True}, org=1)
    g.call("PUT", "/api/org/preferences", {"homeDashboardUID": "infra-projects"}, org=1)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("projects", nargs="*")
    ap.add_argument("--discover", action="store_true", help="every directory in config/grafana/projects/")
    ap.add_argument("--user", help="create/attach a login that belongs to the project's org only (needs exactly one project)")
    ap.add_argument("--reset-password", action="store_true")
    ap.add_argument("--remove", action="store_true", help="delete the project's org")
    a = ap.parse_args()
    names = sorted(set(a.projects) | ({p.name for p in PROJECTS.glob("*") if p.is_dir()} if a.discover else set()))
    if not names:
        sys.exit("no projects: give names or use --discover")
    if a.user and len(names) != 1:
        sys.exit("--user needs exactly one project")
    for n in names:
        if not re.match(NAME_RE, n):
            sys.exit(f"invalid project name {n!r}")
    g = Grafana()
    everyone = known_projects(names)
    for n in names:
        if a.remove:
            o = g.call("GET", "/api/orgs/name/" + urllib.parse.quote(n), ok=(404,))
            if o:
                g.call("DELETE", f"/api/orgs/{o['id']}")
            print(f"{n}: org removed" if o else f"{n}: no org")
            everyone = [x for x in everyone if x != n]
            continue
        org = ensure_org(g, n)
        ensure_datasources(g, org, n)
        count = push_dashboards(g, org, n)
        print(f"{n}: org {org}: 2 filtered data sources (job=~\"{n}/.*\", project=\"{n}\"), {count} dashboards")
        if a.user:
            pw = ensure_user(g, a.user, org, a.reset_password)
            print(f"{n}: login '{a.user}' is an Editor of org '{n}' only" + (f"\n   password (shown once): {pw}" if pw else " (password unchanged)"))
    sync_tenants(everyone)
    hub(g, everyone)
    print("main org: 'Projects' dashboard (home) links " + ", ".join(everyone))


main()
