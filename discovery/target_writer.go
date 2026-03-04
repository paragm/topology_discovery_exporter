package discovery

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var reloadHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
}

// TargetGroup represents a Prometheus file_sd target group.
type TargetGroup struct {
	Targets []string          `yaml:"targets"`
	Labels  map[string]string `yaml:"labels"`
}

// WriteEnrichedTargets reads base target files, enriches them with topology labels,
// and writes the enriched files atomically.
func WriteEnrichedTargets(cfg *Config, hosts []HostConnection, switches []Switch) error {
	// Build hostname lookup map
	hostMap := make(map[string]*HostConnection)
	for i := range hosts {
		hostMap[hosts[i].Hostname] = &hosts[i]
	}

	targetsDir := cfg.Prometheus.TargetsDir
	filesChanged := false

	for _, baseFile := range cfg.BaseTargetFiles {
		basePath := filepath.Join(targetsDir, baseFile)

		// Read base target file
		data, err := os.ReadFile(basePath) //nolint:gosec // basePath from validated config
		if err != nil {
			return fmt.Errorf("read base target file %s: %w", basePath, err)
		}

		var targetGroups []TargetGroup
		if err := yaml.Unmarshal(data, &targetGroups); err != nil {
			return fmt.Errorf("parse base target file %s: %w", basePath, err)
		}

		// Enrich each target group with topology labels
		for i := range targetGroups {
			enrichTargetGroup(&targetGroups[i], hostMap)
		}

		// Determine enriched file name: node_exporter.yml → node_enriched.yml
		enrichedName := enrichedFileName(baseFile)
		enrichedPath := filepath.Join(targetsDir, enrichedName)

		// Validate no path traversal
		absTargets, _ := filepath.Abs(targetsDir)
		absEnriched, _ := filepath.Abs(enrichedPath)
		if !strings.HasPrefix(absEnriched, absTargets+string(filepath.Separator)) && absEnriched != absTargets {
			return fmt.Errorf("path traversal detected: %s escapes %s", enrichedPath, targetsDir)
		}

		// Write atomically: temp file → validate → rename
		changed, err := writeAtomicYAML(enrichedPath, targetGroups)
		if err != nil {
			return fmt.Errorf("write enriched file %s: %w", enrichedPath, err)
		}
		if changed {
			filesChanged = true
		}
	}

	// Trigger Prometheus reload if files changed
	if filesChanged && cfg.Prometheus.ReloadURL != "" {
		if err := triggerPrometheusReload(cfg.Prometheus.ReloadURL); err != nil {
			return fmt.Errorf("trigger prometheus reload: %w", err)
		}
	}

	return nil
}

// enrichTargetGroup adds topology labels to a target group based on hostname matching.
func enrichTargetGroup(tg *TargetGroup, hostMap map[string]*HostConnection) {
	if tg.Labels == nil {
		tg.Labels = make(map[string]string)
	}

	// Try to match targets to discovered hosts
	for _, target := range tg.Targets {
		// Extract hostname from target (host:port or just host)
		hostname := extractHostname(target)

		// Also check the instance label
		if inst, ok := tg.Labels["instance"]; ok && hostname == "" {
			hostname = extractHostname(inst)
		}

		if host, ok := hostMap[hostname]; ok {
			tg.Labels["switch_id"] = host.SwitchID
			tg.Labels["switch_port"] = host.SwitchPort
			tg.Labels["switch_port_index"] = host.SwitchPortIndex
			tg.Labels["uplink_switch_l1"] = host.UplinkSwitchL1
			tg.Labels["uplink_port_l1"] = host.UplinkPortL1
			tg.Labels["uplink_switch_l2"] = host.UplinkSwitchL2
			tg.Labels["uplink_port_l2"] = host.UplinkPortL2
			tg.Labels["network_path"] = host.NetworkPath
			tg.Labels["topology_source"] = host.TopologySource
			tg.Labels["topology_updated"] = time.Now().UTC().Format(time.RFC3339)
			return // First match wins
		}
	}

	// No match found — mark as unknown
	tg.Labels["topology_source"] = "unknown"
	tg.Labels["switch_id"] = "unknown"
}

// extractHostname extracts the hostname from a target string (host:port or just host).
func extractHostname(target string) string {
	// Remove port if present
	if idx := strings.LastIndex(target, ":"); idx > 0 {
		return target[:idx]
	}
	return target
}

// enrichedFileName converts a base target filename to its enriched variant.
// e.g., "node_exporter.yml" → "node_enriched.yml"
//
//	"user_sessions.yml" → "user_sessions_enriched.yml"
func enrichedFileName(baseName string) string {
	ext := filepath.Ext(baseName)
	name := strings.TrimSuffix(baseName, ext)

	// Remove common suffixes before adding _enriched
	name = strings.TrimSuffix(name, "_exporter")

	return name + "_enriched" + ext
}

// writeAtomicYAML writes YAML data atomically using temp file + rename.
// Returns true if the file contents changed.
func writeAtomicYAML(path string, data interface{}) (bool, error) {
	newContent, err := yaml.Marshal(data)
	if err != nil {
		return false, fmt.Errorf("marshal YAML: %w", err)
	}

	// Check if file already exists with same content
	existing, err := os.ReadFile(path) //nolint:gosec // path validated against targetsDir
	if err == nil && string(existing) == string(newContent) {
		return false, nil
	}

	// Write to temp file
	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, ".enriched-*.tmp")
	if err != nil {
		return false, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(newContent); err != nil {
		tmpFile.Close()    //nolint:gosec,errcheck // best-effort close on write failure
		os.Remove(tmpPath) //nolint:gosec,errcheck // best-effort cleanup
		return false, fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close() //nolint:gosec,errcheck // error checked implicitly by rename success

	// Validate YAML by re-parsing
	var validate []TargetGroup
	if err := yaml.Unmarshal(newContent, &validate); err != nil {
		os.Remove(tmpPath) //nolint:gosec,errcheck // best-effort cleanup
		return false, fmt.Errorf("validate YAML: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil { //nolint:gosec // path validated against targetsDir above
		os.Remove(tmpPath) //nolint:gosec,errcheck // best-effort cleanup
		return false, fmt.Errorf("rename temp file: %w", err)
	}

	return true, nil
}

// triggerPrometheusReload sends a POST to Prometheus /-/reload endpoint.
func triggerPrometheusReload(reloadURL string) error {
	u, err := url.Parse(reloadURL)
	if err != nil {
		return fmt.Errorf("invalid reload URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("reload URL must use http or https scheme, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("reload URL must have a host")
	}

	req, err := http.NewRequest(http.MethodPost, reloadURL, nil)
	if err != nil {
		return fmt.Errorf("create reload request: %w", err)
	}
	resp, err := reloadHTTPClient.Do(req) //nolint:gosec // URL validated above (scheme + host check)
	if err != nil {
		return fmt.Errorf("POST to %s: %w", reloadURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("prometheus reload returned status %d", resp.StatusCode)
	}
	return nil
}
