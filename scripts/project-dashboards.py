#!/usr/bin/env python3
"""A full set of Grafana dashboards per project, as JSON in config/grafana/projects/<project>/ (pushed into the project's org by project-access.py).

  scripts/project-dashboards.py gapchi reels-chi                 # generate the standard set for these projects
  scripts/project-dashboards.py --discover                       # ... for every project Prometheus has seen
  scripts/project-dashboards.py --import gapchi ~/p/gapchi/deploy/grafana
                                                                 # add the project's OWN dashboards (JSON files, linted first;
                                                                 # files starting with _ are skipped)
  scripts/project-dashboards.py --discover --prune               # also remove folders of projects that are gone

Standard set (tag "generated", rewritten and re-pushed on every run): overview, http, database, notify, runtime, containers,
custom (a browser for the project's own metrics). Everything else in the folder is the project's: dashboards
imported with --import (files custom-*.json), and dashboards built in the Grafana UI (stored in Grafana, never
touched here). The JSON is pushed into the project's org (own filtered data sources, optional logins) by
scripts/project-access.py, which `make project-dashboards` runs right after this script.
"""
import argparse
import json
import pathlib
import re
import shutil
import subprocess
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
from dashlib import NAME_RE, project_set  # noqa: E402

ROOT = pathlib.Path(__file__).resolve().parent.parent
OUT = ROOT / "config/grafana/projects"


def discover():
    out = subprocess.run(["docker", "compose", "exec", "-T", "prometheus", "wget", "-q", "-O-",
                          "http://127.0.0.1:9090/api/v1/label/job/values"], cwd=ROOT, capture_output=True, text=True)
    if out.returncode != 0:
        sys.exit(f"cannot ask Prometheus (is the stack up?): {out.stderr.strip()}")
    # OTLP jobs are "<project>/<service>"; scraped infra jobs have no slash and are not projects.
    return sorted({j.split("/")[0] for j in json.loads(out.stdout)["data"] if "/" in j})


def check_name(n):
    if not re.match(NAME_RE, n):
        sys.exit(f"invalid project name {n!r}: lowercase letters, digits and '-', at most 24 characters")


def generate(n):
    d = OUT / n
    d.mkdir(parents=True, exist_ok=True)
    keep = set()
    for filename, board, variables in project_set(n):
        board.save(filename, out=d, variables=variables, tags_suffix=())
        keep.add(filename)
    # standard dashboards that no longer exist in the set (renamed/removed between versions)
    for f in d.glob("*.json"):
        if f.name not in keep and not f.name.startswith("custom-"):
            f.unlink()
    print(f"wrote {len(keep)} dashboards to {d}")


def import_custom(n, src):
    src = pathlib.Path(src).expanduser()
    if not src.is_dir():
        sys.exit(f"{src} is not a directory")
    files = sorted(f for f in src.glob("*.json") if not f.name.startswith("_"))  # _example.json etc. are templates, not dashboards
    if files:
        lint = subprocess.run([sys.executable, str(ROOT / "scripts/grafana-lint.py"), *map(str, files)], capture_output=True, text=True)
        print(lint.stdout.rstrip())
        if lint.returncode:
            sys.exit("fix the ERROR lines above (nothing was imported)")
    else:
        print(f"no dashboards in {src} (*.json, files starting with _ are skipped): the project's imported dashboards will be removed")
    d = OUT / n
    d.mkdir(parents=True, exist_ok=True)
    for f in d.glob("custom-*.json"):  # the project's directory is the source of truth
        f.unlink()
    for f in files:
        try:
            dash = json.loads(f.read_text())
        except ValueError as e:
            sys.exit(f"{f}: not valid JSON ({e})")
        if "panels" not in dash and "rows" not in dash:
            sys.exit(f"{f}: not a Grafana dashboard (no panels)")
        stem = re.sub(r"[^a-z0-9]+", "-", f.stem.lower()).strip("-")[:20] or "dash"
        dash.pop("id", None)
        # uids are global in Grafana: derive them from the project so two projects can never overwrite each other
        dash["uid"] = f"p-{n}-c-{stem}"[:40]
        dash["tags"] = sorted(set(dash.get("tags", [])) | {f"project-{n}", "custom"})
        dash.setdefault("title", f"{n} / {stem}")
        dash["links"] = dash.get("links") or [{"type": "dashboards", "title": f"{n} dashboards", "tags": [f"project-{n}"],
                                                "asDropdown": True, "includeVars": False, "keepTime": True}]
        (d / f"custom-{stem}.json").write_text(json.dumps(dash, indent=1) + "\n")
        print(f"imported {f.name} -> {d / ('custom-' + stem + '.json')} (uid {dash['uid']})")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("projects", nargs="*")
    ap.add_argument("--discover", action="store_true")
    ap.add_argument("--prune", action="store_true", help="remove folders of projects not in the list")
    ap.add_argument("--import", dest="imp", nargs=2, metavar=("PROJECT", "DIR"), help="import the project's own dashboard JSON files")
    a = ap.parse_args()
    if a.imp:
        check_name(a.imp[0])
        import_custom(*a.imp)
        return
    names = sorted(set(a.projects) | (set(discover()) if a.discover else set()))
    if not names:
        sys.exit("no projects: give names or use --discover")
    for n in names:
        check_name(n)
    for n in names:
        generate(n)
    if a.prune and OUT.exists():
        for d in OUT.iterdir():
            if d.is_dir() and d.name not in names:
                shutil.rmtree(d)
                print("removed", d)


main()
