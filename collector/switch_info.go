package collector

import (
	"github.com/prometheus/client_golang/prometheus"
)

// SwitchInfoCollector exposes per-switch and per-host topology info metrics.
type SwitchInfoCollector struct {
	config *Config

	switchInfo         *prometheus.Desc
	hostConnectionInfo *prometheus.Desc
	switchPortStatus   *prometheus.Desc
}

// NewSwitchInfoCollector creates a SwitchInfoCollector.
func NewSwitchInfoCollector(cfg *Config) *SwitchInfoCollector {
	return &SwitchInfoCollector{
		config: cfg,
		switchInfo: prometheus.NewDesc(
			"topology_switch_info",
			"Switch identity information. Always 1.",
			[]string{"switch_name", "switch_ip", "switch_role", "sys_desc", "uplink_switch"},
			nil,
		),
		hostConnectionInfo: prometheus.NewDesc(
			"topology_host_connection_info",
			"Host-to-switch connection information. Always 1.",
			[]string{"hostname", "switch_id", "switch_port", "switch_port_index", "uplink_switch_l1", "topology_source"},
			nil,
		),
		switchPortStatus: prometheus.NewDesc(
			"topology_switch_port_status",
			"Switch port operational status. 1=up, 0=down.",
			[]string{"switch_name", "port_id", "port_desc", "oper_status", "connected_host"},
			nil,
		),
	}
}

// Describe implements SubCollector.
func (c *SwitchInfoCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.switchInfo
	ch <- c.hostConnectionInfo
	ch <- c.switchPortStatus
}

// Collect implements SubCollector.
func (c *SwitchInfoCollector) Collect(ch chan<- prometheus.Metric) {
	state := c.config.DiscoveryState
	state.RLock()
	defer state.RUnlock()

	// Switch info metrics
	for _, sw := range state.Switches {
		ch <- prometheus.MustNewConstMetric(
			c.switchInfo, prometheus.GaugeValue, 1,
			sw.Name, sw.IP, sw.Role, sw.SysDesc, sw.UplinkTo,
		)
	}

	// Host connection metrics
	for _, host := range state.Hosts {
		ch <- prometheus.MustNewConstMetric(
			c.hostConnectionInfo, prometheus.GaugeValue, 1,
			host.Hostname, host.SwitchID, host.SwitchPort,
			host.SwitchPortIndex, host.UplinkSwitchL1, host.TopologySource,
		)
	}

	// Switch port status metrics — dedup links to avoid duplicate label sets
	// when a host has multiple NICs reporting identical LLDP data.
	seen := make(map[[5]string]bool)
	for _, link := range state.Links {
		key := [5]string{link.From, link.FromPort, link.FromPortDesc, link.OperStatus, link.To}
		if seen[key] {
			continue
		}
		seen[key] = true
		var status float64
		if link.OperStatus == "up" {
			status = 1
		}
		ch <- prometheus.MustNewConstMetric(
			c.switchPortStatus, prometheus.GaugeValue, status,
			link.From, link.FromPort, link.FromPortDesc, link.OperStatus,
			link.To,
		)
	}
}
