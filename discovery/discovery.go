package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/paragm/topology_discovery_exporter/db"
)

// State holds the current topology discovery state, shared with collectors.
type State struct {
	sync.RWMutex

	Switches           []Switch
	Hosts              []HostConnection
	Links              []Link
	LastRunTime        time.Time
	LastRunDuration    time.Duration
	LastRunSuccess     bool
	RunCount           int64
	ErrorCount         int64
	SwitchErrors       map[string]int64
	TargetFilesWritten int64
}

// RLock/RUnlock are promoted from sync.RWMutex.

// Config holds the exporter configuration loaded from YAML.
type Config struct {
	SNMPExporterURL string           `yaml:"snmp_exporter_url"`
	SNMPModule      string           `yaml:"snmp_module"`
	SNMPAuth        string           `yaml:"snmp_auth"`
	Switches        []SwitchConfig   `yaml:"switches"`
	Prometheus      PrometheusConfig `yaml:"prometheus"`
	BaseTargetFiles []string         `yaml:"base_target_files"`
	DBPath          string           `yaml:"db_path"`
	CacheDir        string           `yaml:"cache_dir"`
}

// SwitchConfig represents a switch definition in the config file.
type SwitchConfig struct {
	Name    string `yaml:"name"`
	Address string `yaml:"address"`
	Role    string `yaml:"role"`
}

// PrometheusConfig holds Prometheus-related settings.
type PrometheusConfig struct {
	ReloadURL  string `yaml:"reload_url"`
	TargetsDir string `yaml:"targets_dir"`
}

// LoadConfig reads and parses the YAML configuration file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path from CLI flag, not user input
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	// Defaults
	if cfg.SNMPExporterURL == "" {
		cfg.SNMPExporterURL = "http://127.0.0.1:9116"
	}
	if cfg.SNMPModule == "" {
		cfg.SNMPModule = "lldp_topology"
	}
	if cfg.DBPath == "" {
		cfg.DBPath = "/var/lib/topology/topology.db"
	}
	if cfg.CacheDir == "" {
		cfg.CacheDir = "/var/lib/topology/cache"
	}

	return cfg, nil
}

// ValidateConfig checks that all required configuration fields are present.
func ValidateConfig(cfg *Config) error {
	if len(cfg.Switches) == 0 {
		return fmt.Errorf("no switches configured")
	}
	if cfg.SNMPAuth == "" {
		return fmt.Errorf("snmp_auth is required")
	}
	if cfg.SNMPExporterURL == "" {
		return fmt.Errorf("snmp_exporter_url is required")
	}
	for i, sw := range cfg.Switches {
		if sw.Name == "" {
			return fmt.Errorf("switch %d: name is required", i)
		}
		if sw.Address == "" {
			return fmt.Errorf("switch %d (%s): address is required", i, sw.Name)
		}
	}
	return nil
}

// switchResult holds the LLDP data scraped from a single switch.
type switchResult struct {
	switchCfg SwitchConfig
	lldp      *SwitchLLDP
	err       error
}

// RunDiscovery performs a full topology discovery cycle.
func RunDiscovery(cfg *Config, state *State, database *db.DB, logger *slog.Logger) error {
	start := time.Now()
	state.Lock()
	state.RunCount++
	state.Unlock()

	logger.Info("discovery run starting", "switches", len(cfg.Switches))

	// Build the set of known switch names for graph building
	knownSwitches := make(map[string]SwitchConfig)
	for _, sw := range cfg.Switches {
		knownSwitches[sw.Name] = sw
	}

	// Query all switches concurrently
	results := make(chan switchResult, len(cfg.Switches))
	var wg sync.WaitGroup

	for _, sw := range cfg.Switches {
		wg.Add(1)
		go func(s SwitchConfig) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			logger.Debug("querying switch", "name", s.Name, "address", s.Address)

			body, err := QuerySNMPExporter(ctx, cfg.SNMPExporterURL, s.Address, cfg.SNMPModule, cfg.SNMPAuth)
			if err != nil {
				results <- switchResult{switchCfg: s, err: fmt.Errorf("query %s: %w", s.Name, err)}
				return
			}

			lldp, err := ParseSNMPResponse(body, s.Name)
			if err != nil {
				results <- switchResult{switchCfg: s, err: fmt.Errorf("parse %s: %w", s.Name, err)}
				return
			}

			results <- switchResult{switchCfg: s, lldp: lldp}
		}(sw)
	}

	// Close results channel when all goroutines finish
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	var allLLDP []switchResult
	var discoveryErrors []error

	for r := range results {
		if r.err != nil {
			logger.Error("switch discovery error", "switch", r.switchCfg.Name, "error", r.err)
			discoveryErrors = append(discoveryErrors, r.err)

			state.Lock()
			state.ErrorCount++
			state.SwitchErrors[r.switchCfg.Name]++
			state.Unlock()
			continue
		}
		logger.Info("switch discovered", "switch", r.switchCfg.Name,
			"neighbors", len(r.lldp.Neighbors), "interfaces", len(r.lldp.Interfaces))
		allLLDP = append(allLLDP, r)
	}

	// Build topology graph from LLDP data
	switches, hosts, links := BuildGraph(allLLDP, knownSwitches, logger)

	// Persist to database (convert discovery types to db types)
	dbSwitches := make([]db.Switch, len(switches))
	for i, s := range switches {
		dbSwitches[i] = db.Switch{Name: s.Name, IP: s.IP, Role: s.Role, SysDesc: s.SysDesc, UplinkTo: s.UplinkTo}
	}
	dbHosts := make([]db.HostConnection, len(hosts))
	for i, h := range hosts {
		dbHosts[i] = db.HostConnection{
			Hostname: h.Hostname, SwitchID: h.SwitchID, SwitchPort: h.SwitchPort,
			SwitchPortIndex: h.SwitchPortIndex, UplinkSwitchL1: h.UplinkSwitchL1,
			UplinkPortL1: h.UplinkPortL1, UplinkSwitchL2: h.UplinkSwitchL2,
			UplinkPortL2: h.UplinkPortL2, NetworkPath: h.NetworkPath, TopologySource: h.TopologySource,
		}
	}
	dbLinks := make([]db.Link, len(links))
	for i, l := range links {
		dbLinks[i] = db.Link{
			From: l.From, FromPort: l.FromPort, FromPortDesc: l.FromPortDesc,
			To: l.To, ToPort: l.ToPort, LinkType: l.LinkType, OperStatus: l.OperStatus,
		}
	}
	if err := database.SaveDiscovery(dbSwitches, dbHosts, dbLinks); err != nil {
		logger.Error("failed to persist discovery", "error", err)
	}

	// Detect changes from previous state
	state.RLock()
	prevHosts := state.Hosts
	state.RUnlock()

	changed := detectChanges(prevHosts, hosts)
	if changed {
		logger.Info("topology changes detected, writing enriched target files")
		if err := WriteEnrichedTargets(cfg, hosts, switches); err != nil {
			logger.Error("failed to write enriched targets", "error", err)
		} else {
			state.Lock()
			state.TargetFilesWritten++
			state.Unlock()

			// Record change in database
			if err := database.RecordChange("topology_update", "all", "topology changed, targets rewritten"); err != nil {
				logger.Error("failed to record change", "error", err)
			}
		}
	} else {
		logger.Info("no topology changes detected")
	}

	// Update state atomically
	duration := time.Since(start)
	state.Lock()
	state.Switches = switches
	state.Hosts = hosts
	state.Links = links
	state.LastRunTime = time.Now()
	state.LastRunDuration = duration
	state.LastRunSuccess = len(discoveryErrors) == 0
	state.Unlock()

	logger.Info("discovery run complete",
		"duration", duration,
		"switches", len(switches),
		"hosts", len(hosts),
		"links", len(links),
		"errors", len(discoveryErrors))

	if len(discoveryErrors) > 0 {
		return fmt.Errorf("%d switch(es) failed discovery", len(discoveryErrors))
	}
	return nil
}

// detectChanges compares previous and current host connections to detect topology changes.
func detectChanges(prev, curr []HostConnection) bool {
	if len(prev) != len(curr) {
		return true
	}

	prevMap := make(map[string]HostConnection)
	for _, h := range prev {
		prevMap[h.Hostname] = h
	}

	for _, h := range curr {
		p, ok := prevMap[h.Hostname]
		if !ok {
			return true
		}
		if p.SwitchID != h.SwitchID || p.SwitchPort != h.SwitchPort || p.SwitchPortIndex != h.SwitchPortIndex {
			return true
		}
	}
	return false
}
