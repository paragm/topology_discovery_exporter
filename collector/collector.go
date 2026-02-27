package collector

import (
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/paragm/topology_discovery_exporter/discovery"
)

// Config holds shared configuration for all sub-collectors.
type Config struct {
	Logger         *slog.Logger
	Hostname       string
	DiscoveryState *discovery.State
}

// SubCollector is implemented by each metric collector.
type SubCollector interface {
	Describe(ch chan<- *prometheus.Desc)
	Collect(ch chan<- prometheus.Metric)
}

// MasterCollector orchestrates all sub-collectors with panic recovery.
type MasterCollector struct {
	config        *Config
	subCollectors []SubCollector
}

// NewMasterCollector creates a MasterCollector with all sub-collectors.
func NewMasterCollector(cfg *Config) *MasterCollector {
	return &MasterCollector{
		config: cfg,
		subCollectors: []SubCollector{
			NewTopologyCollector(cfg),
			NewSwitchInfoCollector(cfg),
		},
	}
}

// Describe implements prometheus.Collector.
func (mc *MasterCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, sc := range mc.subCollectors {
		sc.Describe(ch)
	}
}

// Collect implements prometheus.Collector with panic recovery per sub-collector.
func (mc *MasterCollector) Collect(ch chan<- prometheus.Metric) {
	for _, sc := range mc.subCollectors {
		mc.safeCollect(sc, ch)
	}
}

// safeCollect wraps a sub-collector's Collect in panic recovery.
func (mc *MasterCollector) safeCollect(sc SubCollector, ch chan<- prometheus.Metric) {
	defer func() {
		if r := recover(); r != nil {
			mc.config.Logger.Error("sub-collector panic", "collector", sc, "panic", r)
		}
	}()
	sc.Collect(ch)
}
