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

// normalizeHostname strips the domain suffix from a hostname, returning the short name.
// If domain is empty or the hostname doesn't end with the domain, it returns the hostname unchanged.
func normalizeHostname(hostname, domain string) string {
	if domain == "" || hostname == "" {
		return hostname
	}
	suffix := "." + domain
	if strings.HasSuffix(hostname, suffix) {
		return strings.TrimSuffix(hostname, suffix)
	}
	return hostname
}

// buildHostMap creates a dual-keyed lookup map from LLDP-discovered hosts.
// Each host is keyed by its full LLDP hostname and also by its normalized short name.
// For short-name collisions, the first entry wins.
func buildHostMap(hosts []HostConnection, domain string) map[string]*HostConnection {
	hostMap := make(map[string]*HostConnection, len(hosts)*2)
	for i := range hosts {
		h := &hosts[i]
		// Key by full LLDP hostname
		hostMap[h.Hostname] = h
		// Also key by short name (if different and not already taken)
		short := normalizeHostname(h.Hostname, domain)
		if short != "" && short != h.Hostname {
			if _, exists := hostMap[short]; !exists {
				hostMap[short] = h
			}
		}
	}
	return hostMap
}

// applyTopologyLabels copies topology fields from a HostConnection into target group labels.
func applyTopologyLabels(tg *TargetGroup, host *HostConnection, source string) {
	tg.Labels["switch_id"] = host.SwitchID
	tg.Labels["switch_port"] = host.SwitchPort
	tg.Labels["switch_port_index"] = host.SwitchPortIndex
	tg.Labels["uplink_switch_l1"] = host.UplinkSwitchL1
	tg.Labels["uplink_port_l1"] = host.UplinkPortL1
	tg.Labels["uplink_switch_l2"] = host.UplinkSwitchL2
	tg.Labels["uplink_port_l2"] = host.UplinkPortL2
	tg.Labels["network_path"] = host.NetworkPath
	tg.Labels["topology_source"] = source
	tg.Labels["topology_updated"] = time.Now().UTC().Format(time.RFC3339)
}

// WriteEnrichedTargets reads base target files, enriches them with topology labels,
// and writes the enriched files atomically.
func WriteEnrichedTargets(cfg *Config, hosts []HostConnection, switches []Switch) error {
	hostMap := buildHostMap(hosts, cfg.Domain)

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
			enrichTargetGroup(&targetGroups[i], hostMap, cfg.Domain)
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

// lookupHost finds a HostConnection by trying the name directly, then normalized.
func lookupHost(hostMap map[string]*HostConnection, name, domain string) *HostConnection {
	if host, ok := hostMap[name]; ok {
		return host
	}
	normalized := normalizeHostname(name, domain)
	if normalized != name {
		if host, ok := hostMap[normalized]; ok {
			return host
		}
	}
	return nil
}

// resolveParentHost applies inherited topology labels from a parent_host label.
// Returns true if a parent was found and labels were applied.
func resolveParentHost(tg *TargetGroup, hostMap map[string]*HostConnection, domain string) bool {
	parentHost, ok := tg.Labels["parent_host"]
	if !ok || parentHost == "" {
		return false
	}
	host := lookupHost(hostMap, parentHost, domain)
	if host == nil {
		return false
	}
	applyTopologyLabels(tg, host, "inherited:"+host.Hostname)
	tg.Labels["network_path"] = "vm→" + host.NetworkPath
	return true
}

// enrichTargetGroup adds topology labels to a target group using a matching cascade:
// 1. Direct/normalized match by hostname
// 2. parent_host fallback (VM inherits parent's topology)
// 3. Unknown (no match)
func enrichTargetGroup(tg *TargetGroup, hostMap map[string]*HostConnection, domain string) {
	if tg.Labels == nil {
		tg.Labels = make(map[string]string)
	}

	// Try to match targets to discovered hosts
	for _, target := range tg.Targets {
		hostname := extractHostname(target)

		// Also check the instance label
		if inst, ok := tg.Labels["instance"]; ok && hostname == "" {
			hostname = extractHostname(inst)
		}

		if host := lookupHost(hostMap, hostname, domain); host != nil {
			applyTopologyLabels(tg, host, host.TopologySource)
			return
		}
	}

	// parent_host fallback: VM inherits parent's topology
	if resolveParentHost(tg, hostMap, domain) {
		return
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
