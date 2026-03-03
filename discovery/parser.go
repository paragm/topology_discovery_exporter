package discovery

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/prometheus/common/expfmt"

	dto "github.com/prometheus/client_model/go"
)

// LLDPNeighbor represents a single LLDP neighbor entry from a switch.
type LLDPNeighbor struct {
	LocalPortID    string
	LocalPortDesc  string
	RemoteSysName  string
	RemotePortID   string
	RemotePortDesc string
	RemoteSysDesc  string
}

// InterfaceInfo holds interface details from SNMP IF-MIB metrics.
type InterfaceInfo struct {
	Index      string
	Descr      string
	OperStatus string // "up" or "down"
	Alias      string
}

// SwitchLLDP holds all LLDP and interface data parsed from a single switch.
type SwitchLLDP struct {
	SysName    string
	SysDesc    string
	Neighbors  []LLDPNeighbor
	Interfaces []InterfaceInfo
}

// extractSysDescr extracts the sysDescr label from the metric families.
func extractSysDescr(families map[string]*dto.MetricFamily) string {
	mf, ok := families["sysDescr"]
	if !ok {
		return ""
	}
	for _, m := range mf.GetMetric() {
		if m.GetGauge() != nil || m.GetUntyped() != nil {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "sysDescr" {
					return lp.GetValue()
				}
			}
		}
	}
	return ""
}

// extractLLDPNeighbors extracts all LLDP neighbor data from the metric families.
func extractLLDPNeighbors(families map[string]*dto.MetricFamily) []LLDPNeighbor {
	neighborMap := make(map[string]*LLDPNeighbor)

	extractLLDPLabels(families, "lldpRemSysName", neighborMap, func(n *LLDPNeighbor, labels map[string]string) {
		n.RemoteSysName = labels["lldpRemSysName"]
	})
	extractLLDPLabels(families, "lldpRemPortId", neighborMap, func(n *LLDPNeighbor, labels map[string]string) {
		n.RemotePortID = labels["lldpRemPortId"]
	})
	extractLLDPLabels(families, "lldpRemPortDesc", neighborMap, func(n *LLDPNeighbor, labels map[string]string) {
		n.RemotePortDesc = labels["lldpRemPortDesc"]
	})
	extractLLDPLabels(families, "lldpRemSysDesc", neighborMap, func(n *LLDPNeighbor, labels map[string]string) {
		n.RemoteSysDesc = labels["lldpRemSysDesc"]
	})
	extractLLDPLabels(families, "lldpLocPortId", neighborMap, func(n *LLDPNeighbor, labels map[string]string) {
		n.LocalPortID = labels["lldpLocPortId"]
	})
	extractLLDPLabels(families, "lldpLocPortDesc", neighborMap, func(n *LLDPNeighbor, labels map[string]string) {
		n.LocalPortDesc = labels["lldpLocPortDesc"]
	})

	var neighbors []LLDPNeighbor
	for _, n := range neighborMap {
		neighbors = append(neighbors, *n)
	}
	return neighbors
}

// getOrCreateIF returns the InterfaceInfo for the given index, creating it if needed.
func getOrCreateIF(ifMap map[string]*InterfaceInfo, idx string) *InterfaceInfo {
	if iface, ok := ifMap[idx]; ok {
		return iface
	}
	iface := &InterfaceInfo{Index: idx}
	ifMap[idx] = iface
	return iface
}

// extractInterfaces extracts IF-MIB interface information from the metric families.
func extractInterfaces(families map[string]*dto.MetricFamily) []InterfaceInfo {
	ifMap := make(map[string]*InterfaceInfo)

	if mf, ok := families["ifDescr"]; ok {
		for _, m := range mf.GetMetric() {
			labels := labelMap(m)
			if idx := labels["ifIndex"]; idx != "" {
				getOrCreateIF(ifMap, idx).Descr = labels["ifDescr"]
			}
		}
	}

	if mf, ok := families["ifOperStatus"]; ok {
		for _, m := range mf.GetMetric() {
			labels := labelMap(m)
			if idx := labels["ifIndex"]; idx != "" {
				iface := getOrCreateIF(ifMap, idx)
				if metricValue(m) == 1 {
					iface.OperStatus = "up"
				} else {
					iface.OperStatus = "down"
				}
			}
		}
	}

	if mf, ok := families["ifAlias"]; ok {
		for _, m := range mf.GetMetric() {
			labels := labelMap(m)
			if idx := labels["ifIndex"]; idx != "" {
				getOrCreateIF(ifMap, idx).Alias = labels["ifAlias"]
			}
		}
	}

	var interfaces []InterfaceInfo
	for _, iface := range ifMap {
		interfaces = append(interfaces, *iface)
	}
	return interfaces
}

// ParseSNMPResponse parses Prometheus text format response from the SNMP exporter
// into structured LLDP and interface data.
func ParseSNMPResponse(body []byte, switchName string) (*SwitchLLDP, error) {
	parser := expfmt.TextParser{}
	families, err := parser.TextToMetricFamilies(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parse prometheus text format: %w", err)
	}

	return &SwitchLLDP{
		SysName:    switchName,
		SysDesc:    extractSysDescr(families),
		Neighbors:  extractLLDPNeighbors(families),
		Interfaces: extractInterfaces(families),
	}, nil
}

// extractLLDPLabels extracts LLDP neighbor data from a metric family.
// The neighbor key is derived from lldpRemTimeMark + lldpRemLocalPortNum + lldpRemIndex labels.
func extractLLDPLabels(families map[string]*dto.MetricFamily, metricName string,
	neighbors map[string]*LLDPNeighbor, setter func(*LLDPNeighbor, map[string]string)) {

	mf, ok := families[metricName]
	if !ok {
		return
	}

	for _, m := range mf.GetMetric() {
		labels := labelMap(m)
		key := neighborKey(labels)
		if key == "" {
			continue
		}
		if _, ok := neighbors[key]; !ok {
			neighbors[key] = &LLDPNeighbor{}
		}
		setter(neighbors[key], labels)
	}
}

// neighborKey builds a unique key for an LLDP neighbor entry from its index labels.
func neighborKey(labels map[string]string) string {
	parts := []string{
		labels["lldpRemTimeMark"],
		labels["lldpRemLocalPortNum"],
		labels["lldpRemIndex"],
	}
	key := strings.Join(parts, "|")
	if key == "||" {
		return ""
	}
	return key
}

// labelMap converts a metric's label pairs to a string map.
func labelMap(m *dto.Metric) map[string]string {
	result := make(map[string]string)
	for _, lp := range m.GetLabel() {
		result[lp.GetName()] = lp.GetValue()
	}
	return result
}

// metricValue extracts the numeric value from a metric regardless of type.
func metricValue(m *dto.Metric) float64 {
	if g := m.GetGauge(); g != nil {
		return g.GetValue()
	}
	if c := m.GetCounter(); c != nil {
		return c.GetValue()
	}
	if u := m.GetUntyped(); u != nil {
		return u.GetValue()
	}
	return 0
}
