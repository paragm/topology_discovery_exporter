# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.2.0] - 2026-02-28

### Added
- Topology history tracking in SQLite database
- Discovery run metadata persistence
- Change detection for target file writes

### Changed
- Improved LLDP neighbor parsing robustness
- Enhanced multi-NIC host deduplication logic

## [1.1.0] - 2026-02-15

### Added
- Target file enrichment with topology labels
- Automatic Prometheus reload on target file changes
- JSON API endpoint at `/api/v1/topology`

### Fixed
- Race condition in concurrent SNMP queries

## [1.0.0] - 2026-02-01

### Added
- Initial release
- SNMP/LLDP-based network topology discovery
- Concurrent switch polling via snmp_exporter
- Topology graph building (switches, hosts, links)
- Prometheus metrics for discovery health
- Per-switch and per-host topology info metrics
- Switch port status tracking
- SQLite persistence with WAL mode
- Prometheus target file enrichment with topology labels
- Configurable discovery interval
- Systemd service with security hardening
- Environment variable overrides
