package collector

import (
	"github.com/prometheus/client_golang/prometheus"
)

// TopologyCollector exposes discovery health metrics.
type TopologyCollector struct {
	config *Config

	lastSuccessTimestamp    *prometheus.Desc
	lastDurationSeconds     *prometheus.Desc
	runsTotal               *prometheus.Desc
	errorsTotal             *prometheus.Desc
	switchesDiscovered      *prometheus.Desc
	hostsDiscovered         *prometheus.Desc
	linksDiscovered         *prometheus.Desc
	targetFilesWrittenTotal *prometheus.Desc
	up                      *prometheus.Desc
}

// NewTopologyCollector creates a TopologyCollector.
func NewTopologyCollector(cfg *Config) *TopologyCollector {
	return &TopologyCollector{
		config: cfg,
		lastSuccessTimestamp: prometheus.NewDesc(
			"topology_discovery_last_success_timestamp",
			"Unix timestamp of last successful discovery run.",
			nil, nil,
		),
		lastDurationSeconds: prometheus.NewDesc(
			"topology_discovery_last_duration_seconds",
			"Duration of last discovery run in seconds.",
			nil, nil,
		),
		runsTotal: prometheus.NewDesc(
			"topology_discovery_runs_total",
			"Total number of discovery runs.",
			nil, nil,
		),
		errorsTotal: prometheus.NewDesc(
			"topology_discovery_errors_total",
			"Total number of discovery errors.",
			[]string{"switch"}, nil,
		),
		switchesDiscovered: prometheus.NewDesc(
			"topology_discovery_switches_discovered",
			"Number of switches discovered in the last run.",
			nil, nil,
		),
		hostsDiscovered: prometheus.NewDesc(
			"topology_discovery_hosts_discovered",
			"Number of hosts discovered in the last run.",
			nil, nil,
		),
		linksDiscovered: prometheus.NewDesc(
			"topology_discovery_links_discovered",
			"Number of links discovered in the last run.",
			nil, nil,
		),
		targetFilesWrittenTotal: prometheus.NewDesc(
			"topology_discovery_target_files_written_total",
			"Total number of enriched target file writes.",
			nil, nil,
		),
		up: prometheus.NewDesc(
			"topology_discovery_up",
			"Whether the last discovery run succeeded (1=success, 0=failure).",
			nil, nil,
		),
	}
}

// Describe implements SubCollector.
func (c *TopologyCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.lastSuccessTimestamp
	ch <- c.lastDurationSeconds
	ch <- c.runsTotal
	ch <- c.errorsTotal
	ch <- c.switchesDiscovered
	ch <- c.hostsDiscovered
	ch <- c.linksDiscovered
	ch <- c.targetFilesWrittenTotal
	ch <- c.up
}

// Collect implements SubCollector.
func (c *TopologyCollector) Collect(ch chan<- prometheus.Metric) {
	state := c.config.DiscoveryState
	state.RLock()
	defer state.RUnlock()

	if state.LastRunSuccess {
		ch <- prometheus.MustNewConstMetric(
			c.lastSuccessTimestamp, prometheus.GaugeValue,
			float64(state.LastRunTime.Unix()),
		)
	}

	ch <- prometheus.MustNewConstMetric(
		c.lastDurationSeconds, prometheus.GaugeValue,
		state.LastRunDuration.Seconds(),
	)

	ch <- prometheus.MustNewConstMetric(
		c.runsTotal, prometheus.CounterValue,
		float64(state.RunCount),
	)

	// Per-switch errors
	for sw, count := range state.SwitchErrors {
		ch <- prometheus.MustNewConstMetric(
			c.errorsTotal, prometheus.CounterValue,
			float64(count), sw,
		)
	}

	ch <- prometheus.MustNewConstMetric(
		c.switchesDiscovered, prometheus.GaugeValue,
		float64(len(state.Switches)),
	)

	ch <- prometheus.MustNewConstMetric(
		c.hostsDiscovered, prometheus.GaugeValue,
		float64(len(state.Hosts)),
	)

	ch <- prometheus.MustNewConstMetric(
		c.linksDiscovered, prometheus.GaugeValue,
		float64(len(state.Links)),
	)

	ch <- prometheus.MustNewConstMetric(
		c.targetFilesWrittenTotal, prometheus.CounterValue,
		float64(state.TargetFilesWritten),
	)

	var upValue float64
	if state.LastRunSuccess {
		upValue = 1
	}
	ch <- prometheus.MustNewConstMetric(
		c.up, prometheus.GaugeValue,
		upValue,
	)
}
