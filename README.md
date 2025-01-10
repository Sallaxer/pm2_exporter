# pm2_exporter

Go PM2 Exporter for Prometheus

This exporter calls "pm2 jlist" periodically (default every 30 seconds),
parses the returned JSON, and exposes the data as Prometheus metrics.

Metrics include:
- pm2_status (1 if online, 0 otherwise, with label "status")
- pm2_branch_info (1 if branch is non-empty, 0 otherwise, with labels "branch", "revision", and "comment")
- pm2_memory_bytes
- pm2_cpu_percent
- pm2_uptime_seconds (calculated from pm_uptime)
- pm2_restart_count
- pm2_created_at_timestamp

All metrics also have "process" and "pid" labels to differentiate processes.

## Building and running

### Build

```
make
```

### Running

```
./pm2_exporter <flags>
```

### Flags

Name                                       | Description
-------------------------------------------|--------------------------------------------------------------------------------------------------
web.listen-address                         | Address to listen on for web interface and telemetry. (Default `:9966`)
web.scrape-interval                        | How often (seconds) to call "pm2 jlist" (Default `30`)
help                                       | Show help text