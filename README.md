# Topology Discovery Exporter

[![CI](https://github.com/paragm/topology_discovery_exporter/actions/workflows/ci.yml/badge.svg)](https://github.com/paragm/topology_discovery_exporter/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/paragm/topology_discovery_exporter)](https://goreportcard.com/report/github.com/paragm/topology_discovery_exporter)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

Prometheus exporter that discovers network topology via SNMP/LLDP and enriches Prometheus target files with topology labels.

## Overview

The Topology Discovery Exporter periodically polls network switches via [snmp_exporter](https://github.com/prometheus/snmp_exporter), parses LLDP neighbor data, builds a topology graph, and:

- **Exposes discovery metrics** — run status, switch/host/link counts, per-switch errors
- **Enriches Prometheus targets** — adds `switch_id`, `switch_port`, `uplink_switch_l1`, `network_path` labels to file_sd target files
- **Persists topology** — stores discovered topology in SQLite for history and change detection
- **Triggers Prometheus reload** — automatically reloads Prometheus when target files change

### Architecture

```
┌─────────────┐     SNMP      ┌───────────────┐     HTTP      ┌──────────────┐
│  Switches   │◄──────────────│ snmp_exporter  │◄─────────────│  topology    │
│ (LLDP data) │               └───────────────┘               │  _discovery  │
└─────────────┘                                                │  _exporter   │
                                                               │              │
                              ┌───────────────┐  file_sd write │              │
                              │  Prometheus   │◄───────────────│              │
                              │  (targets/)   │   POST reload  │              │
                              └───────────────┘                └──────────────┘
```

## Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `topology_discovery_up` | Gauge | — | 1 if last discovery succeeded, 0 if failed |
| `topology_discovery_last_success_timestamp` | Gauge | — | Unix timestamp of last successful run |
| `topology_discovery_last_duration_seconds` | Gauge | — | Duration of last discovery run |
| `topology_discovery_runs_total` | Counter | — | Total number of discovery runs |
| `topology_discovery_errors_total` | Counter | `switch` | Errors per switch |
| `topology_discovery_switches_discovered` | Gauge | — | Switches discovered in last run |
| `topology_discovery_hosts_discovered` | Gauge | — | Hosts discovered in last run |
| `topology_discovery_links_discovered` | Gauge | — | Links discovered in last run |
| `topology_discovery_target_files_written_total` | Counter | — | Total enriched target file writes |
| `topology_switch_info` | Gauge | `switch_name`, `switch_ip`, `switch_role`, `sys_desc`, `uplink_switch` | Per-switch identity (always 1) |
| `topology_host_connection_info` | Gauge | `hostname`, `switch_id`, `switch_port`, `switch_port_index`, `uplink_switch_l1`, `topology_source` | Per-host connection (always 1) |
| `topology_switch_port_status` | Gauge | `switch_name`, `port_id`, `port_desc`, `connected_host` | Port status (1=up, 0=down) |

## Installation

### From Source

```bash
git clone https://github.com/paragm/topology_discovery_exporter.git
cd topology_discovery_exporter
make build
sudo make install
```

### Systemd Service

```bash
sudo cp examples/systemd/topology_discovery_exporter.service /etc/systemd/system/
sudo cp examples/systemd/topology_discovery_exporter.env /etc/default/topology-discovery-exporter
sudo cp examples/config.yml.example /opt/topology/config.yml
sudo systemctl daemon-reload
sudo systemctl enable --now topology_discovery_exporter
```

## Configuration

### CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-web.listen-address` | `:10042` | Address to listen on for metrics |
| `-config.file` | `/opt/topology/config.yml` | Path to configuration file |
| `-discovery.interval` | `15m` | Interval between discovery runs (min: 30s) |
| `-log.level` | `info` | Log level: debug, info, warn, error |
| `-version` | — | Print version and exit |

### Environment Variables

| Variable | Description |
|----------|-------------|
| `PORT` | Override listen port |
| `LOG_LEVEL` | Override log level |
| `CONFIG_FILE` | Override config file path |
| `DISCOVERY_INTERVAL` | Override discovery interval |

### Configuration File

```yaml
snmp_exporter_url: http://127.0.0.1:9116
snmp_module: lldp_topology
snmp_auth: my_snmp_profile

switches:
  - name: core-sw01
    address: 192.168.0.1
    role: core
  - name: access-sw01
    address: 192.168.0.10
    role: access

prometheus:
  reload_url: http://127.0.0.1:9090/-/reload
  targets_dir: /etc/prometheus/targets

base_target_files:
  - /etc/prometheus/targets/nodes.yml

db_path: /var/lib/topology/topology.db
cache_dir: /var/lib/topology/cache
```

## Endpoints

| Path | Description |
|------|-------------|
| `/metrics` | Prometheus metrics |
| `/api/v1/topology` | JSON topology state |
| `/-/healthy` | Health check |
| `/-/ready` | Readiness check |
| `/` | Landing page |

## Building

```bash
make build          # Build for current platform
make build-linux    # Cross-compile for Linux amd64
make test           # Run tests with race detection
make lint           # Run golangci-lint
make coverage       # Generate HTML coverage report
```

## License

Apache License 2.0 — see [LICENSE](LICENSE) for details.
