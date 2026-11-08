#!/usr/bin/env python3
"""Regenerates the Grafana dashboards in config/grafana/dashboards/ (they are committed; rerun after editing).

grafana.com (community dashboards) is not reachable from every network, so the dashboards are
generated from PromQL here instead. Only the metrics exposed by this stack's own exporters are used.
"""
import json
import pathlib

OUT = pathlib.Path(__file__).resolve().parent.parent / "config/grafana/dashboards"
DS = {"type": "prometheus", "uid": "prometheus"}


class Board:
    def __init__(self, uid, title, tags):
        self.uid, self.title, self.tags = uid, title, tags
        self.panels, self.x, self.y, self.rowh, self.pid = [], 0, 0, 0, 0

    def _place(self, w, h):
        if self.x + w > 24:
            self.x, self.y, self.rowh = 0, self.y + self.rowh, 0
        pos = {"x": self.x, "y": self.y, "w": w, "h": h}
        self.x += w
        self.rowh = max(self.rowh, h)
        return pos

    def _base(self, kind, title, w, h, targets, unit, desc):
        self.pid += 1
        return {
            "id": self.pid, "type": kind, "title": title, "description": desc, "datasource": DS,
            "gridPos": self._place(w, h),
            "targets": [
                {"refId": chr(65 + i), "datasource": DS, "expr": e, "legendFormat": legend}
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

    def ts(self, title, targets, unit="short", w=8, h=7, desc="", stack=False, minv=None, maxv=None):
        p = self._base("timeseries", title, w, h, targets, unit, desc)
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

    def stat(self, title, expr, unit="short", w=4, h=4, desc="", steps=None, mappings=None):
        p = self._base("stat", title, w, h, [(expr, "")], unit, desc)
        d = p["fieldConfig"]["defaults"]
        d["thresholds"] = {"mode": "absolute", "steps": steps or [{"color": "green", "value": None}]}
        if mappings:
            d["mappings"] = mappings
        p["options"] = {"reduceOptions": {"calcs": ["lastNotNull"]}, "colorMode": "background", "graphMode": "none"}
        self.panels.append(p)

    def save(self, filename):
        board = {
            "uid": self.uid, "title": self.title, "tags": self.tags + ["infrastructure"], "schemaVersion": 39,
            "version": 1, "editable": False, "refresh": "30s", "time": {"from": "now-6h", "to": "now"},
            "timezone": "browser", "panels": self.panels, "templating": {"list": []}, "annotations": {"list": []},
        }
        (OUT / filename).write_text(json.dumps(board, indent=1) + "\n")


OK_DOWN = [{"type": "value", "options": {"0": {"text": "DOWN"}, "1": {"text": "UP"}}}]
GREEN_RED = [{"color": "red", "value": None}, {"color": "green", "value": 1}]
USAGE = [{"color": "green", "value": None}, {"color": "orange", "value": 70}, {"color": "red", "value": 90}]
LOW_FREE = [{"color": "red", "value": None}, {"color": "orange", "value": 10}, {"color": "green", "value": 20}]
FS = 'fstype!~"tmpfs|overlay|squashfs|ramfs"'
CN = 'name!=""'

# ---------------------------------------------------------------- server
b = Board("infra-server", "Server", ["host"])
b.stat("Uptime", "time() - node_boot_time_seconds", "s")
b.stat("CPU", '100 - avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100', "percent", steps=USAGE)
b.stat("Memory used", "100 * (1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)", "percent", steps=USAGE)
b.stat("Load (5m) per core", 'node_load5 / scalar(count(node_cpu_seconds_total{mode="idle"}))', "short",
       steps=[{"color": "green", "value": None}, {"color": "orange", "value": 1}, {"color": "red", "value": 1.5}])
b.stat("Root disk free", f'100 * node_filesystem_avail_bytes{{mountpoint="/",{FS}}} / node_filesystem_size_bytes{{mountpoint="/",{FS}}}',
       "percent", steps=LOW_FREE)
b.stat("Firing alerts", 'count(ALERTS{alertstate="firing",alertname!="Watchdog"}) or vector(0)', "short",
       steps=[{"color": "green", "value": None}, {"color": "red", "value": 1}])
b.row("CPU & load")
b.ts("CPU by mode", [('sum by (mode) (rate(node_cpu_seconds_total{mode!="idle"}[5m])) * 100 / scalar(count(node_cpu_seconds_total{mode="idle"}))', "{{mode}}")],
     "percent", w=12, stack=True, maxv=100)
b.ts("Load average", [("node_load1", "1m"), ("node_load5", "5m"), ("node_load15", "15m"),
                      ('count(node_cpu_seconds_total{mode="idle"})', "cores")], w=12)
b.row("Memory")
b.ts("Memory", [("node_memory_MemTotal_bytes - node_memory_MemAvailable_bytes", "used (excl. cache)"),
                ("node_memory_Cached_bytes + node_memory_Buffers_bytes", "page cache"),
                ("node_memory_MemAvailable_bytes", "available"), ("node_memory_MemTotal_bytes", "total")], "bytes", w=12)
b.ts("Swap & OOM", [("node_memory_SwapTotal_bytes - node_memory_SwapFree_bytes", "swap used"),
                    ("rate(node_vmstat_pswpin[5m]) * 4096", "swap in B/s"), ("rate(node_vmstat_pswpout[5m]) * 4096", "swap out B/s"),
                    ("increase(node_vmstat_oom_kill[1h])", "OOM kills (1h)")], "bytes", w=12)
b.row("Disk")
b.ts("Filesystem free", [(f"100 * node_filesystem_avail_bytes{{{FS}}} / node_filesystem_size_bytes{{{FS}}}", "{{mountpoint}}")],
     "percent", w=8, minv=0, maxv=100)
b.ts("Disk throughput", [("rate(node_disk_read_bytes_total[5m])", "{{device}} read"),
                         ("rate(node_disk_written_bytes_total[5m])", "{{device}} write")], "Bps", w=8)
b.ts("Disk busy / IOPS", [("rate(node_disk_io_time_seconds_total[5m]) * 100", "{{device}} busy %"),
                          ("rate(node_disk_reads_completed_total[5m]) + rate(node_disk_writes_completed_total[5m])", "{{device}} IOPS")], w=8)
b.row("Network & kernel")
b.ts("Network throughput", [("sum(rate(node_network_receive_bytes_total[5m])) * 8", "in"),
                            ("sum(rate(node_network_transmit_bytes_total[5m])) * 8", "out")], "bps", w=8)
b.ts("TCP", [("node_netstat_Tcp_CurrEstab", "established"), ("node_sockstat_TCP_tw", "time-wait"),
             ("rate(node_netstat_Tcp_RetransSegs[5m])", "retransmits/s")], w=8)
b.ts("conntrack & file descriptors", [("100 * node_nf_conntrack_entries / node_nf_conntrack_entries_limit", "conntrack %"),
                                      ("100 * node_filefd_allocated / node_filefd_maximum", "file descriptors %")], "percent", w=8)
b.save("server.json")

# ---------------------------------------------------------------- containers
b = Board("infra-containers", "Containers", ["docker"])
b.ts("Memory (working set) by container", [(f"container_memory_working_set_bytes{{{CN}}}", "{{name}}")], "bytes", w=12, h=9)
b.ts("Memory vs limit", [(f"100 * container_memory_working_set_bytes{{{CN}}} / (container_spec_memory_limit_bytes{{{CN}}} > 0)", "{{name}}")],
     "percent", w=12, h=9, desc="Containers above 90% for long are alerted (ContainerMemoryNearLimit).", maxv=100)
b.ts("CPU by container (cores)", [(f"sum by (name) (rate(container_cpu_usage_seconds_total{{{CN}}}[5m]))", "{{name}}")], w=12, h=9)
b.ts("CPU throttling", [(f"rate(container_cpu_cfs_throttled_periods_total{{{CN}}}[5m]) / rate(container_cpu_cfs_periods_total{{{CN}}}[5m])", "{{name}}")],
     "percentunit", w=12, h=9)
b.ts("Network in/out by container", [(f"sum by (name) (rate(container_network_receive_bytes_total{{{CN}}}[5m]))", "{{name}} in"),
                                     (f"sum by (name) (rate(container_network_transmit_bytes_total{{{CN}}}[5m]))", "{{name}} out")], "Bps", w=12, h=9)
b.ts("Restarts / OOM kills (1h)", [(f"changes(container_start_time_seconds{{{CN}}}[1h])", "{{name}} restarts"),
                                   (f"increase(container_oom_events_total{{{CN}}}[1h])", "{{name}} OOM")], w=12, h=9)
b.save("containers.json")

# ---------------------------------------------------------------- postgres
b = Board("infra-postgres", "PostgreSQL", ["database"])
b.stat("Status", "pg_up", mappings=OK_DOWN, steps=GREEN_RED)
b.stat("Connections", "sum(pg_stat_activity_count)")
b.stat("Connections % of max", "100 * sum(pg_stat_activity_count) / max(pg_settings_max_connections)", "percent", steps=USAGE)
b.stat("Cache hit ratio", "100 * sum(rate(pg_stat_database_blks_hit[5m])) / (sum(rate(pg_stat_database_blks_hit[5m])) + sum(rate(pg_stat_database_blks_read[5m])))",
       "percent", steps=LOW_FREE)
b.stat("Total DB size", "sum(pg_database_size_bytes)", "bytes")
b.stat("Longest transaction", 'max(pg_stat_activity_max_tx_duration{state!="idle"})', "s",
       steps=[{"color": "green", "value": None}, {"color": "orange", "value": 60}, {"color": "red", "value": 300}])
b.ts("Connections by state", [("sum by (state) (pg_stat_activity_count)", "{{state}}")], w=8, stack=True)
b.ts("Transactions / s", [("sum by (datname) (rate(pg_stat_database_xact_commit[5m]))", "{{datname}} commit"),
                          ("sum(rate(pg_stat_database_xact_rollback[5m]))", "rollback (all)")], w=8)
b.ts("Rows / s", [("sum(rate(pg_stat_database_tup_returned[5m]))", "returned"), ("sum(rate(pg_stat_database_tup_fetched[5m]))", "fetched"),
                  ("sum(rate(pg_stat_database_tup_inserted[5m]))", "inserted"), ("sum(rate(pg_stat_database_tup_updated[5m]))", "updated"),
                  ("sum(rate(pg_stat_database_tup_deleted[5m]))", "deleted")], w=8)
b.ts("Database size", [("pg_database_size_bytes", "{{datname}}")], "bytes", w=8)
b.ts("Locks / deadlocks / conflicts", [("sum by (mode) (pg_locks_count)", "locks {{mode}}"),
                                       ("sum(increase(pg_stat_database_deadlocks[5m]))", "deadlocks (5m)"),
                                       ("sum(increase(pg_stat_database_conflicts[5m]))", "conflicts (5m)")], w=8)
b.ts("Temp files (work_mem too small if growing)", [("sum(rate(pg_stat_database_temp_bytes[5m]))", "temp bytes/s")], "Bps", w=8)
b.ts("Background writer (buffers/s)", [("rate(pg_stat_bgwriter_buffers_clean_total[5m])", "cleaned by bgwriter"),
                                       ("rate(pg_stat_bgwriter_buffers_alloc_total[5m])", "allocated")], w=12)
b.ts("Top queries by total time (pg_stat_statements)", [("topk(8, sum by (datname, queryid) (rate(pg_stat_statements_seconds_total[10m])))", "{{datname}} {{queryid}}")],
     "s", w=12, desc="Take the queryid to: SELECT query FROM pg_stat_statements WHERE queryid = <id>;")
b.save("postgres.json")

# ---------------------------------------------------------------- redis + rabbitmq
b = Board("infra-queues", "Redis & RabbitMQ", ["redis", "rabbitmq"])
b.row("Redis")
b.stat("Status", "redis_up", mappings=OK_DOWN, steps=GREEN_RED)
b.stat("Memory % of max", "100 * redis_memory_used_bytes / (redis_memory_max_bytes > 0)", "percent", steps=USAGE)
b.stat("Clients", "redis_connected_clients")
b.stat("Hit rate", "100 * rate(redis_keyspace_hits_total[5m]) / (rate(redis_keyspace_hits_total[5m]) + rate(redis_keyspace_misses_total[5m]))", "percent", steps=LOW_FREE)
b.stat("Keys", "sum(redis_db_keys)")
b.stat("Evicted (1h)", "increase(redis_evicted_keys_total[1h])")
b.ts("Memory", [("redis_memory_used_bytes", "used"), ("redis_memory_max_bytes", "maxmemory")], "bytes", w=8)
b.ts("Commands / s", [("rate(redis_commands_processed_total[5m])", "commands")], w=8)
b.ts("Evictions / expirations / rejected conns", [("rate(redis_evicted_keys_total[5m])", "evicted/s"), ("rate(redis_expired_keys_total[5m])", "expired/s"),
                                                 ("rate(redis_rejected_connections_total[5m])", "rejected conns/s")], w=8)
b.row("RabbitMQ")
b.stat("Messages ready", "sum(rabbitmq_queue_messages_ready)", steps=[{"color": "green", "value": None}, {"color": "orange", "value": 1000}, {"color": "red", "value": 10000}])
b.stat("Unacked", "sum(rabbitmq_queue_messages_unacked)")
b.stat("Connections", "sum(rabbitmq_connections)")
b.stat("Channels", "sum(rabbitmq_channels)")
b.stat("Consumers", "sum(rabbitmq_consumers)")
b.stat("Alarms", "max(rabbitmq_alarms_memory_used_watermark) + max(rabbitmq_alarms_free_disk_space_watermark)", mappings=[
    {"type": "value", "options": {"0": {"text": "none"}}}], steps=[{"color": "green", "value": None}, {"color": "red", "value": 1}])
b.ts("Message rates", [("sum(rate(rabbitmq_channel_messages_published_total[5m]))", "published"),
                       ("sum(rate(rabbitmq_channel_messages_delivered_total[5m]))", "delivered"),
                       ("sum(rate(rabbitmq_channel_messages_acked_total[5m]))", "acked"),
                       ("sum(rate(rabbitmq_channel_messages_redelivered_total[5m]))", "redelivered")], w=8)
b.ts("Queue depth", [("sum(rabbitmq_queue_messages_ready)", "ready"), ("sum(rabbitmq_queue_messages_unacked)", "unacked")], w=8)
b.ts("Memory / watermark", [("rabbitmq_process_resident_memory_bytes", "resident"), ("rabbitmq_resident_memory_limit_bytes", "limit")], "bytes", w=8)
b.save("redis-rabbitmq.json")

# ---------------------------------------------------------------- edge
b = Board("infra-edge", "Edge & uptime", ["nginx", "uptime"])
b.stat("nginx", "nginx_up", mappings=OK_DOWN, steps=GREEN_RED)
b.stat("Active connections", "nginx_connections_active")
b.stat("Requests / s", "rate(nginx_http_requests_total[5m])", "reqps")
b.stat("Endpoints up", "sum(probe_success)")
b.stat("Endpoints down", "count(probe_success == 0) or vector(0)", steps=[{"color": "green", "value": None}, {"color": "red", "value": 1}])
b.ts("nginx connections", [("nginx_connections_active", "active"), ("nginx_connections_reading", "reading"),
                           ("nginx_connections_writing", "writing"), ("nginx_connections_waiting", "waiting (keepalive)")], w=12)
b.ts("nginx requests / s", [("rate(nginx_http_requests_total[5m])", "requests"), ("rate(nginx_connections_accepted[5m])", "accepted conns")], w=12)
b.ts("External probe: success", [("probe_success", "{{instance}}")], w=12, minv=0, maxv=1)
b.ts("External probe: response time", [("probe_duration_seconds", "{{instance}}")], "s", w=12)
b.ts("External probe: HTTP status", [("probe_http_status_code", "{{instance}}")], w=12)
b.ts("TLS certificate days left", [("(probe_ssl_earliest_cert_expiry - time()) / 86400", "{{instance}}")], "d", w=12)
b.save("edge.json")

# ---------------------------------------------------------------- applications (OTel RED)
b = Board("infra-apps", "Applications (OpenTelemetry)", ["apps", "otel"])
b.row("HTTP servers - from the OTel SDK metric http.server.request.duration (job = service.name)")
b.ts("Request rate by service", [("sum by (job) (rate(http_server_request_duration_seconds_count[5m]))", "{{job}}")], "reqps", w=8)
b.ts("5xx error ratio by service", [('sum by (job) (rate(http_server_request_duration_seconds_count{http_response_status_code=~"5.."}[5m])) '
                                     "/ sum by (job) (rate(http_server_request_duration_seconds_count[5m]))", "{{job}}")], "percentunit", w=8)
b.ts("Latency p95 by service", [("histogram_quantile(0.95, sum by (job, le) (rate(http_server_request_duration_seconds_bucket[5m])))", "{{job}}")], "s", w=8)
b.ts("Latency p99 by service", [("histogram_quantile(0.99, sum by (job, le) (rate(http_server_request_duration_seconds_bucket[5m])))", "{{job}}")], "s", w=12)
b.ts("Requests by status code", [("sum by (http_response_status_code) (rate(http_server_request_duration_seconds_count[5m]))", "{{http_response_status_code}}")], "reqps", w=12, stack=True)
b.row("Telemetry pipeline")
b.ts("Spans accepted / sent / refused", [("sum(rate(otelcol_receiver_accepted_spans[5m]))", "accepted"), ("sum(rate(otelcol_exporter_sent_spans[5m]))", "sent to Tempo (after sampling)"),
                                         ("sum(rate(otelcol_receiver_refused_spans[5m]))", "refused (memory limiter)")], w=8)
b.ts("Metric points accepted / refused", [("sum(rate(otelcol_receiver_accepted_metric_points[5m]))", "accepted"), ("sum(rate(otelcol_receiver_refused_metric_points[5m]))", "refused")], w=8)
b.ts("Collector & Tempo memory", [('container_memory_working_set_bytes{name=~".*otel-collector.*"}', "collector"), ('container_memory_working_set_bytes{name=~".*tempo.*"}', "tempo")], "bytes", w=8)
b.save("applications.json")

print("wrote", len(list(OUT.glob("*.json"))), "dashboards to", OUT)
