#!/usr/bin/env python3
"""Shared dashboard builder for gen-dashboards.py and project-dashboards.py."""
import json
import pathlib

OUT = pathlib.Path(__file__).resolve().parent.parent / "config/grafana/dashboards"
DS = {"type": "prometheus", "uid": "prometheus"}
# containers (cadvisor) and uptime probes: same uid in every Grafana org, restricted per project in project orgs
DS_INFRA = {"type": "prometheus", "uid": "prometheus-infra"}


class Board:
    def __init__(self, uid, title, tags):
        self.uid, self.title, self.tags = uid, title, tags
        self.panels, self.x, self.y, self.rowh, self.pid = [], 0, 0, 0, 0
        self.links = []

    def _place(self, w, h):
        if self.x + w > 24:
            self.x, self.y, self.rowh = 0, self.y + self.rowh, 0
        pos = {"x": self.x, "y": self.y, "w": w, "h": h}
        self.x += w
        self.rowh = max(self.rowh, h)
        return pos

    def _base(self, kind, title, w, h, targets, unit, desc, ds=None):
        ds = ds or DS
        self.pid += 1
        return {
            "id": self.pid, "type": kind, "title": title, "description": desc, "datasource": ds,
            "gridPos": self._place(w, h),
            "targets": [
                {"refId": chr(65 + i), "datasource": ds, "expr": e, "legendFormat": legend}
                for i, (e, legend) in enumerate(targets)
            ],
            "fieldConfig": {"defaults": {"unit": unit}, "overrides": []},
        }

    def row(self, title):
        self.x, self.y, self.rowh = 0, self.y + self.rowh, 0
        self.pid += 1
        self.panels.append({"id": self.pid, "type": "row", "title": title, "collapsed": False,
                            "gridPos": {"x": 0, "y": self.y, "w": 24, "h": 1}, "panels": []})
        self.y += 1
        self.rowh = 0

    def ts(self, title, targets, unit="short", w=8, h=7, desc="", stack=False, minv=None, maxv=None, ds=None):
        p = self._base("timeseries", title, w, h, targets, unit, desc, ds)
        c = {"fillOpacity": 25 if stack else 8, "lineWidth": 1, "showPoints": "never"}
        if stack:
            c["stacking"] = {"mode": "normal", "group": "A"}
        p["fieldConfig"]["defaults"]["custom"] = c
        if minv is not None:
            p["fieldConfig"]["defaults"]["min"] = minv
        if maxv is not None:
            p["fieldConfig"]["defaults"]["max"] = maxv
        p["options"] = {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "multi", "sort": "desc"}}
        self.panels.append(p)

    def stat(self, title, expr, unit="short", w=4, h=4, desc="", steps=None, mappings=None, ds=None):
        p = self._base("stat", title, w, h, [(expr, "")], unit, desc, ds)
        d = p["fieldConfig"]["defaults"]
        d["thresholds"] = {"mode": "absolute", "steps": steps or [{"color": "green", "value": None}]}
        if mappings:
            d["mappings"] = mappings
        p["options"] = {"reduceOptions": {"calcs": ["lastNotNull"]}, "colorMode": "background", "graphMode": "none"}
        self.panels.append(p)

    def table(self, title, expr, w=24, h=9, desc="", columns=None):
        """Instant-query table; columns renames/hides labels: {"Time": hide, ...} is not needed, Value is kept."""
        p = self._base("table", title, w, h, [(expr, "")], "short", desc)
        p["targets"][0].update({"instant": True, "range": False, "format": "table"})
        p["transformations"] = [{"id": "organize", "options": {"excludeByName": {"Time": True}, "renameByName": columns or {}}}]
        p["options"] = {"showHeader": True, "sortBy": [{"displayName": "Value", "desc": True}]}
        self.panels.append(p)

    def tempo_traces(self, title, query, ds_uid, w=24, h=9, desc="", limit=20):
        """TraceQL search as a table: one row per trace (id, start, root service/operation, duration); the id opens the trace."""
        self.pid += 1
        ds = {"type": "tempo", "uid": ds_uid}
        self.panels.append({
            "id": self.pid, "type": "table", "title": title, "description": desc, "datasource": ds, "gridPos": self._place(w, h),
            "targets": [{"refId": "A", "datasource": ds, "queryType": "traceql", "query": query, "limit": limit, "tableType": "traces"}],
            "fieldConfig": {"defaults": {}, "overrides": []}, "options": {"showHeader": True}})

    def text(self, content, w=24, h=4, title=""):
        self.pid += 1
        self.panels.append({"id": self.pid, "type": "text", "title": title, "gridPos": self._place(w, h),
                            "options": {"mode": "markdown", "content": content}})

    def save(self, filename, out=None, variables=None, tags_suffix=("infrastructure",), editable=False):
        board = {
            "uid": self.uid, "title": self.title, "tags": self.tags + list(tags_suffix), "schemaVersion": 39,
            "version": 1, "editable": False, "refresh": "30s", "time": {"from": "now-6h", "to": "now"},
            "timezone": "browser", "panels": self.panels, "templating": {"list": variables or []}, "annotations": {"list": []},
            "links": self.links,
        }
        if editable:
            board["editable"] = True
        (pathlib.Path(out or OUT) / filename).write_text(json.dumps(board, indent=1) + "\n")




# ---------------------------------------------------------------- project dashboards
# Every gokit service exports OTel metrics with service.namespace=<project> and service.name=<service>;
# Prometheus's OTLP receiver turns that into job="<project>/<service>" (just "<service>" with no namespace).
# So one set of panels, filtered by job, shows any project. Built from it:
#   template   : "Project overview" with project + service pickers (admin view of any project)  -> infra-project
#   per project: a Grafana FOLDER <name> with the dashboards below, project fixed               -> p-<name>-<kind>
# Dashboards generated here carry the tag "generated"; anything else in the folder is the project's own.
NAME_RE = r"^[a-z0-9][a-z0-9-]{0,23}$"  # becomes part of a dashboard uid (max 40 chars) and a folder name
STANDARD = "http_.*|db_.*|go_.*|process_.*|notify_.*|target_info|otel_.*|scrape_.*|up|probe_.*"


class Ctx:
    def __init__(self, project):
        self.fixed = project is not None
        self.P = project if self.fixed else "$project"
        self.J = 'job=~"$service"'  # $service always holds full job values, so every panel filters on it alone
        self.C = f'project="{self.P}"'  # compose project (relabelled in prometheus.yml), same label the probes have


def s_summary(b, x):
    J, P = x.J, x.P
    b.stat("Services reporting", f'count(count by (job) (target_info{{{J},instance=""}})) or vector(0)', w=4)
    b.stat("Requests / s", f"sum(rate(http_server_request_duration_seconds_count{{{J}}}[5m])) or vector(0)", "reqps", w=4)
    b.stat("5xx ratio", f'(sum(rate(http_server_request_duration_seconds_count{{{J},http_response_status_code=~"5.."}}[5m])) '
                        f"/ sum(rate(http_server_request_duration_seconds_count{{{J}}}[5m]))) or vector(0)", "percentunit", w=4,
           steps=[{"color": "green", "value": None}, {"color": "orange", "value": 0.01}, {"color": "red", "value": 0.05}])
    b.stat("Latency p95", f"histogram_quantile(0.95, sum by (le) (rate(http_server_request_duration_seconds_bucket{{{J}}}[5m])))", "s", w=4,
           steps=[{"color": "green", "value": None}, {"color": "orange", "value": 0.5}, {"color": "red", "value": 1}])
    b.stat("Goroutines", f"sum(go_goroutine_count{{{J}}})", w=4)
    b.stat("Endpoints down", f'count(probe_success{{project="{P}"}} == 0) or vector(0)', w=4,
           steps=[{"color": "green", "value": None}, {"color": "red", "value": 1}], ds=DS_INFRA)


def s_http(b, x, brief=False):
    J = x.J
    b.row("HTTP")
    b.ts("Request rate by service", [(f"sum by (job) (rate(http_server_request_duration_seconds_count{{{J}}}[5m]))", "{{job}}")], "reqps", w=8)
    b.ts("5xx ratio by service", [(f'sum by (job) (rate(http_server_request_duration_seconds_count{{{J},http_response_status_code=~"5.."}}[5m])) '
                                   f"/ sum by (job) (rate(http_server_request_duration_seconds_count{{{J}}}[5m]))", "{{job}}")], "percentunit", w=8)
    b.ts("Latency p50 / p95 / p99", [(f"histogram_quantile({q}, sum by (le) (rate(http_server_request_duration_seconds_bucket{{{J}}}[5m])))", f"p{int(q * 100)}")
                                     for q in (0.5, 0.95, 0.99)], "s", w=8)
    if brief:
        return
    b.ts("Requests by status code", [(f"sum by (http_response_status_code) (rate(http_server_request_duration_seconds_count{{{J}}}[5m]))", "{{http_response_status_code}}")],
         "reqps", w=8, stack=True)
    b.ts("Busiest routes", [(f"topk(8, sum by (http_request_method, http_route) (rate(http_server_request_duration_seconds_count{{{J}}}[5m])))", "{{http_request_method}} {{http_route}}")],
         "reqps", w=8)
    b.ts("Slowest routes (p95)", [(f"topk(8, histogram_quantile(0.95, sum by (le, http_request_method, http_route) (rate(http_server_request_duration_seconds_bucket{{{J}}}[5m]))))",
                                   "{{http_request_method}} {{http_route}}")], "s", w=8)
    b.ts("Error rate by route (5xx / s)", [(f'topk(8, sum by (http_request_method, http_route) (rate(http_server_request_duration_seconds_count{{{J},http_response_status_code=~"5.."}}[5m])))',
                                            "{{http_request_method}} {{http_route}}")], "reqps", w=12)
    b.ts("Request / response size", [(f"sum(rate(http_server_request_body_size_bytes_sum{{{J}}}[5m]))", "request bytes/s"),
                                     (f"sum(rate(http_server_response_body_size_bytes_sum{{{J}}}[5m]))", "response bytes/s")], "Bps", w=12)
    b.table("Routes (last 5m)", f"sum by (http_request_method, http_route) (rate(http_server_request_duration_seconds_count{{{J}}}[5m]))", h=10,
            columns={"http_request_method": "Method", "http_route": "Route", "Value": "req/s"})


def s_db(b, x):
    J = x.J
    b.row("Database (pgx)")
    b.ts("Operations / s", [(f"sum by (pgx_operation_type) (rate(db_client_operation_duration_seconds_count{{{J}}}[5m]))", "{{pgx_operation_type}}")], "ops", w=8)
    b.ts("Latency p95 by operation", [(f"histogram_quantile(0.95, sum by (le, pgx_operation_type) (rate(db_client_operation_duration_seconds_bucket{{{J}}}[5m])))",
                                       "{{pgx_operation_type}}")], "s", w=8)
    b.ts("Errors / s", [(f"sum by (pgx_operation_type) (rate(db_client_operation_errors_total{{{J}}}[5m]))", "{{pgx_operation_type}}")], "ops", w=8)
    b.ts("Latency p50 / p99 (queries)", [(f'histogram_quantile({q}, sum by (le) (rate(db_client_operation_duration_seconds_bucket{{{J},pgx_operation_type="query"}}[5m])))', f"p{int(q * 100)}")
                                         for q in (0.5, 0.99)], "s", w=12)
    b.ts("Pool: acquire latency p95", [(f'histogram_quantile(0.95, sum by (le) (rate(db_client_operation_duration_seconds_bucket{{{J},pgx_operation_type="acquire"}}[5m])))', "acquire")], "s", w=12)


def s_notify(b, x):
    J = x.J
    b.row("Notifications (gokit/notify; empty when the project sends none)")
    b.ts("Deliveries / s by outcome", [(f"sum by (notify_channel, notify_outcome) (rate(notify_deliveries_total{{{J}}}[5m]))", "{{notify_channel}} {{notify_outcome}}")], "ops", w=8, stack=True)
    b.ts("Delivery time p95", [(f"histogram_quantile(0.95, sum by (le, notify_channel) (rate(notify_delivery_duration_seconds_bucket{{{J}}}[5m])))", "{{notify_channel}}")], "s", w=8)
    b.ts("Provider attempts by result", [(f"sum by (notify_provider, notify_result) (rate(notify_delivery_attempts_total{{{J}}}[5m]))", "{{notify_provider}} {{notify_result}}")], "ops", w=8)
    b.ts("Queue: enqueued", [(f"sum by (notify_outcome) (rate(notify_enqueued_total{{{J}}}[5m]))", "{{notify_outcome}}")], "ops", w=8)
    b.ts("Queue: processed", [(f"sum by (notify_outcome) (rate(notify_jobs_processed_total{{{J}}}[5m]))", "{{notify_outcome}}")], "ops", w=8)
    b.ts("Queue wait p95", [(f"histogram_quantile(0.95, sum by (le) (rate(notify_queue_wait_seconds_bucket{{{J}}}[5m])))", "wait")], "s", w=8)


def s_runtime(b, x, brief=False):
    J = x.J
    b.row("Runtime")
    b.ts("Heap in use vs GC goal", [(f"sum by (job) (go_memory_used_bytes{{{J}}})", "{{job}} in use"), (f"sum by (job) (go_memory_gc_goal_bytes{{{J}}})", "{{job}} GC goal")], "bytes", w=8)
    b.ts("Allocation rate", [(f"sum by (job) (rate(go_memory_allocated_bytes_total{{{J}}}[5m]))", "{{job}}")], "Bps", w=8)
    b.ts("Goroutines", [(f"sum by (job) (go_goroutine_count{{{J}}})", "{{job}}")], w=8)
    if brief:
        return
    b.ts("Allocations / s", [(f"sum by (job) (rate(go_memory_allocations_total{{{J}}}[5m]))", "{{job}}")], w=8)
    b.ts("Memory limit & GOGC", [(f"max by (job) (go_memory_limit_bytes{{{J}}})", "{{job}} GOMEMLIMIT"), (f"max by (job) (go_config_gogc_percent{{{J}}})", "{{job}} GOGC %")], w=8)
    b.ts("Processor limit", [(f"max by (job) (go_processor_limit{{{J}}})", "{{job}}")], w=8)
    b.table("Instances", f'target_info{{{J},instance=""}}', h=8,
            columns={"job": "Service", "host_name": "Host", "process_pid": "PID", "process_runtime_version": "Go", "service_version": "Version", "telemetry_sdk_version": "OTel SDK"})


def s_containers(b, x):
    C = x.C
    b.row("Containers (cadvisor)")
    b.ts("Container CPU (cores)", [(f'sum by (name) (rate(container_cpu_usage_seconds_total{{{C}}}[5m]))', "{{name}}")], w=8, ds=DS_INFRA)
    b.ts("Container memory", [(f'sum by (name) (container_memory_working_set_bytes{{{C}}})', "{{name}}")], "bytes", w=8, ds=DS_INFRA)
    b.ts("Container network", [(f'sum(rate(container_network_receive_bytes_total{{{C}}}[5m]))', "in"), (f'sum(rate(container_network_transmit_bytes_total{{{C}}}[5m]))', "out")], "Bps", w=8, ds=DS_INFRA)
    b.ts("Container restarts (1h)", [(f'sum by (name) (changes(container_start_time_seconds{{{C}}}[1h]))', "{{name}}")], w=12, ds=DS_INFRA)
    b.ts("Container disk I/O", [(f'sum(rate(container_fs_reads_bytes_total{{{C}}}[5m]))', "read"), (f'sum(rate(container_fs_writes_bytes_total{{{C}}}[5m]))', "write")], "Bps", w=12, ds=DS_INFRA)


def s_edge(b, x):
    P = x.P
    b.row("Edge probes (project label from targets/http-probes.yml)")
    b.ts("Probe success", [(f'probe_success{{project="{P}"}}', "{{instance}}")], w=8, minv=0, maxv=1, ds=DS_INFRA)
    b.ts("Probe response time", [(f'probe_duration_seconds{{project="{P}"}}', "{{instance}}")], "s", w=8, ds=DS_INFRA)
    b.ts("TLS certificate days left", [(f'(probe_ssl_earliest_cert_expiry{{project="{P}"}} - time()) / 86400', "{{instance}}")], "d", w=8, ds=DS_INFRA)
    b.ts("HTTP status", [(f'probe_http_status_code{{project="{P}"}}', "{{instance}}")], w=24, ds=DS_INFRA)


def s_traces(b, x, tempo_uid=None):
    """Latency per request from SPAN METRICS (exact: computed from every span, before sampling; isolated like any metric) and,
    when tempo_uid is given, the traces themselves from Tempo (sampled: all errors, all slow ones, a share of the rest)."""
    J = x.J
    S = f'{J},span_kind="SPAN_KIND_SERVER"'  # a server span = one request handled by the service
    B = "traces_span_metrics_duration_milliseconds_bucket"
    C = "traces_span_metrics_calls_total"
    D = "traces_span_metrics_duration_milliseconds_sum"
    b.stat("Requests / s", f"sum(rate({C}{{{S}}}[5m])) or vector(0)", "reqps", w=4)
    b.stat("Latency p50", f"histogram_quantile(0.5, sum by (le) (rate({B}{{{S}}}[5m])))", "ms", w=4)
    b.stat("Latency p95", f"histogram_quantile(0.95, sum by (le) (rate({B}{{{S}}}[5m])))", "ms", w=4,
           steps=[{"color": "green", "value": None}, {"color": "orange", "value": 500}, {"color": "red", "value": 1000}])
    b.stat("Latency p99", f"histogram_quantile(0.99, sum by (le) (rate({B}{{{S}}}[5m])))", "ms", w=4,
           steps=[{"color": "green", "value": None}, {"color": "orange", "value": 1000}, {"color": "red", "value": 2500}])
    b.stat("Failed requests", f'(sum(rate({C}{{{S},status_code="STATUS_CODE_ERROR"}}[5m])) / sum(rate({C}{{{S}}}[5m]))) or vector(0)', "percentunit", w=4,
           steps=[{"color": "green", "value": None}, {"color": "orange", "value": 0.01}, {"color": "red", "value": 0.05}])
    b.stat("Spans / s (all kinds)", f"sum(rate({C}{{{J}}}[5m])) or vector(0)", "ops", w=4)

    b.row("How long does a request take (every request, from span metrics)")
    b.ts("Request duration p50 / p95 / p99", [(f"histogram_quantile({q}, sum by (le) (rate({B}{{{S}}}[5m])))", f"p{int(q * 100)}") for q in (0.5, 0.95, 0.99)], "ms", w=12)
    b.ts("Request duration p95 by service", [(f"histogram_quantile(0.95, sum by (le, job) (rate({B}{{{S}}}[5m])))", "{{job}}")], "ms", w=12)
    b.ts("Request duration p95 by request", [(f"topk(10, histogram_quantile(0.95, sum by (le, span_name) (rate({B}{{{S}}}[5m]))))",
                                            "{{span_name}}")], "ms", w=12)
    b.ts("Requests / s by request", [(f"topk(10, sum by (span_name) (rate({C}{{{S}}}[5m])))", "{{span_name}}")], "reqps", w=12)
    b.table("Slowest requests (p95 over the last 15 min)", f"topk(25, histogram_quantile(0.95, sum by (le, span_name) (rate({B}{{{S}}}[15m]))))", h=9,
            columns={"span_name": "Request (method + route)", "Value": "p95 (ms)"})

    b.row("Where the time goes (all spans: database, outbound calls, internal work)")
    b.ts("Time spent by operation (ms of span time per second)", [(f"topk(10, sum by (span_name, span_kind) (rate({D}{{{J}}}[5m])))", "{{span_kind}} {{span_name}}")], "ms", w=12, stack=True,
         desc="Spans nest, so the total is larger than wall-clock time; compare operations with each other.")
    b.ts("Duration p95 of non-request spans", [(f'topk(10, histogram_quantile(0.95, sum by (le, span_name) (rate({B}{{{J},span_kind!="SPAN_KIND_SERVER"}}[5m]))))', "{{span_name}}")], "ms", w=12)
    b.ts("Failed spans / s by operation", [(f'topk(10, sum by (span_name) (rate({C}{{{J},status_code="STATUS_CODE_ERROR"}}[5m])))', "{{span_name}}")], "ops", w=12)
    b.ts("Spans / s by kind", [(f"sum by (span_kind) (rate({C}{{{J}}}[5m]))", "{{span_kind}}")], "ops", w=12, stack=True)

    if tempo_uid:
        scope = f'resource.service.namespace="{x.P}" && ' if not x.fixed else ""
        b.row("The traces themselves (Tempo keeps every error, every slow trace and a share of the rest)")
        b.text("Click a Trace ID to open the waterfall of that request. `Min duration` and `Request contains` filter the first table.", h=2)
        b.tempo_traces("Slowest recent requests", f'{{ {scope}kind=server && duration > ${{min_duration}} && name =~ ".*${{route}}.*" }} | select(span.http.response.status_code, span.url.path)', tempo_uid)
        b.tempo_traces("Failed requests", f'{{ {scope}kind=server && status=error }} | select(span.http.response.status_code, span.url.path)', tempo_uid, w=12)
        b.tempo_traces("Latest requests", f'{{ {scope}kind=server }} | select(span.http.response.status_code, span.url.path)', tempo_uid, w=12)


def s_custom(b, x):
    J = x.J
    b.text("**Your own metrics.** Everything the project exports through OpenTelemetry that is not part of the standard set (HTTP, database, "
           "notify, Go runtime) is listed here. Pick metrics in the **Metric** box to plot them. To keep a view, build your own dashboard in this "
           "folder (New dashboard; Grafana saves it here), or commit JSON to the project's `deploy/grafana/` and run `make project-dashboards-import`.",
           h=3)
    b.table("Custom metrics of this project", f'count by (__name__, job) ({{{J},__name__!~"{STANDARD}"}})', h=10,
            columns={"__name__": "Metric", "job": "Service", "Value": "series"})
    b.ts("Selected metrics (raw)", [(f'{{{J},__name__=~"$metric"}}', "")], w=12, h=9)
    b.ts("Selected metrics (rate / s, for counters)", [(f'rate({{{J},__name__=~"$metric"}}[5m])', "")], w=12, h=9)


def query_var(name, label, query, regex="", multi=False, sort=1):
    v = {"name": name, "label": label, "type": "query", "datasource": DS, "query": {"query": query, "refId": "v"},
         "refresh": 2, "sort": sort, "regex": regex, "multi": multi, "includeAll": multi}
    if multi:
        v["allValue"] = ".*"
    return v


def service_vars(project):
    # target_info exists once per OTel service (resource attributes), so it lists the reporting services; scraped
    # exporters (otel-collector, ...) carry an instance label, OTLP pushes do not, hence instance="".
    fixed = project is not None
    P = project if fixed else "$project"
    variables = [query_var("service", "Service", f'label_values(target_info{{instance="",job=~"{P}(/.*)?"}}, job)', multi=True)]
    if not fixed:
        # job is "<project>/<service>"; the regex keeps only the project part.
        variables.insert(0, query_var("project", "Project", 'label_values(target_info{instance=""}, job)', regex="/^([^\\/]+)(?:\\/.*)?$/"))
    return variables


def project_board(project=None):
    """The all-in-one board. project=None: template with a $project picker; a name: fixed to that project."""
    x = Ctx(project)
    b = Board(f"project-{project}" if x.fixed else "infra-project", f"Project: {project}" if x.fixed else "Project overview", ["project", "otel"])
    s_summary(b, x)
    s_http(b, x)
    s_db(b, x)
    s_notify(b, x)
    s_runtime(b, x)
    s_containers(b, x)
    s_edge(b, x)
    s_traces(b, x)  # span-metric panels only: the template has no per-project Tempo tenant
    return b, service_vars(project)


# (file stem, title suffix, builder). Per project folder: one dashboard each, linked together by tag.
def project_set(project):
    """Dashboards of one project's folder: [(filename, Board, variables)]."""
    x = Ctx(project)
    out = []

    def make(kind, title, build, extra_vars=()):
        b = Board(f"p-{project}-{kind}", f"{project} / {title}", [f"project-{project}", "generated", "otel"])
        build(b)
        b.links = [{"type": "dashboards", "title": f"{project} dashboards", "tags": [f"project-{project}"], "asDropdown": True,
                    "includeVars": False, "keepTime": True, "icon": "external link"}]
        out.append((f"{kind}.json", b, service_vars(project) + list(extra_vars)))

    def overview(b):
        s_summary(b, x)
        s_http(b, x, brief=True)
        b.row("Database")
        b.ts("Operations / s", [(f"sum by (pgx_operation_type) (rate(db_client_operation_duration_seconds_count{{{x.J}}}[5m]))", "{{pgx_operation_type}}")], "ops", w=8)
        b.ts("Latency p95 by operation", [(f"histogram_quantile(0.95, sum by (le, pgx_operation_type) (rate(db_client_operation_duration_seconds_bucket{{{x.J}}}[5m])))",
                                           "{{pgx_operation_type}}")], "s", w=8)
        b.ts("Errors / s", [(f"sum by (pgx_operation_type) (rate(db_client_operation_errors_total{{{x.J}}}[5m]))", "{{pgx_operation_type}}")], "ops", w=8)
        s_runtime(b, x, brief=True)
        s_edge(b, x)

    make("overview", "Overview", overview)
    make("http", "HTTP API", lambda b: (s_summary(b, x), s_http(b, x)))
    make("database", "Database", lambda b: s_db(b, x))
    make("notify", "Notifications", lambda b: s_notify(b, x))
    make("runtime", "Go runtime", lambda b: s_runtime(b, x))
    make("containers", "Containers & uptime", lambda b: (s_containers(b, x), s_edge(b, x)))
    traces_vars = [{"name": "min_duration", "label": "Min duration", "type": "textbox", "query": "100ms", "current": {"text": "100ms", "value": "100ms"},
                    "options": [{"selected": True, "text": "100ms", "value": "100ms"}]},
                   {"name": "route", "label": "Request contains", "type": "textbox", "query": "", "current": {"text": "", "value": ""},
                    "options": [{"selected": True, "text": "", "value": ""}]}]
    make("traces", "Traces & latency", lambda b: s_traces(b, x, tempo_uid="tempo"), extra_vars=traces_vars)
    metric_var = query_var("metric", "Metric", f'label_values({{instance="",job=~"{project}(/.*)?",__name__!~"{STANDARD}"}}, __name__)', multi=True)
    metric_var["allValue"] = "NOMATCH_SELECT_A_METRIC"  # "All" would plot every series; the table above lists them instead
    metric_var["includeAll"] = False
    make("custom", "Custom metrics", lambda b: s_custom(b, x), extra_vars=[metric_var])
    return out
