# pi1 incident — Grafana 13 OOM loop (2026-10-19)

**Host:** `pi1.alintech.com.au` (serves `graphs.alintech.com.au`). Raspberry Pi, armv7l, 1 GB RAM, no swap,
Raspbian 10. Also runs MySQL, PostgreSQL, Zabbix server 4.2.8 and Apache.

## What happened

| Time (UTC) | Event |
|---|---|
| 10-18 23:23 | Grafana upgraded 9.5.1 → 13.2.4 via apt (maintainer-approved). Healthy after ~100 s, 184 MB RSS. |
| 10-19 09:20 | First OOM kill of the main `grafana` process (anon RSS ~258 MB). systemd restarts it. |
| 10-19 10:21, 10:52, 11:29 | Further OOM kills (258–279 MB). |
| 10-19 ~12:00 | Web View 1.0.0 (CI-signed, c891cb7) copied in; `systemctl restart` timed out (box already thrashing). |
| 10-19 16:02 | Plugin first registered (after an OOM-driven restart). OOM kills continue at 16:01, 17:00. |
| 10-19 17:26 | Diagnosis: load ~200, 62% iowait, 31 MB/s reads, 332 MB available. No swap, so page-cache eviction makes everything re-read from disk; Apache, Zabbix and other processes stall. |

**Root cause:** Grafana 13.2.4's main process grows to ~260–300 MB on this host, which no longer fits next to
the databases. Grafana 9.5.1 used ~80 MB. The Web View plugin is not the cause: the loop began 7 hours before
it was installed, and its backend process idles at ~2.5 MB RSS.

## Mitigation applied (maintainer-approved, Grafana-only)

`/etc/systemd/system/grafana-server.service.d/pi1-memory.conf`:

```ini
[Service]
Environment=GOMEMLIMIT=220MiB
Environment=GF_PLUGINS_DISABLE_PLUGINS=elasticsearch,grafana-postgresql-datasource,grafana-pyroscope-datasource,influxdb,jaeger,loki,mssql,mysql,opentsdb,prometheus,stackdriver,tempo,zipkin
```

The only data source on pi1 is Zabbix, so the 13 disabled bundled data-source plugins are unused.
Undo: delete the file, `systemctl daemon-reload`, restart Grafana.
Maintainer instruction: if this does not resolve it, stop Grafana and ask what to do next.

## Rollback (if needed)

Stop Grafana; `apt-get install grafana=9.5.1`; restore `/var/backups/grafana-9.5.1-backup-20261018.tgz`
(`/etc/grafana`, `/var/lib/grafana`, including Zabbix app 4.1.2 and Clock 1.3.1); start Grafana.
The Web View plugin needs Grafana >= 12.4, so it cannot run after a rollback.

## Lessons

- Check memory headroom (not just disk) before a major Grafana upgrade on a small host. Grafana 13 needs
  roughly 3× the memory of 9.5.
- On a host without swap, an OOM loop shows up as I/O thrash (high load, high iowait, hung SSH), not as low
  free memory.
