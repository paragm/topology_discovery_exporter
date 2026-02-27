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

// ParseSNMPResponse parses Prometheus text format response from the SNMP exporter
// into structured LLDP and interface data.
func ParseSNMPResponse(body []byte, switchName string) (*SwitchLLDP, error) {
	parser := expfmt.TextParser{}
	families, err := parser.TextToMetricFamilies(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parse prometheus text format: %w", err)
	}

	result := &SwitchLLDP{
		SysName: switchName,
	}

	// Extract sysDescr
	if mf, ok := families["sysDescr"]; ok {
		for _, m := range mf.GetMetric() {
			if m.GetGauge() != nil || m.GetUntyped() != nil {
				for _, lp := range m.GetLabel() {
					if lp.GetName() == "sysDescr" {
						result.SysDesc = lp.GetValue()
					}
				}
			}
		}
	}

	// Extract LLDP neighbor data
	// SNMP exporter exposes these with labels containing the LLDP info
	neighborMap := make(map[string]*LLDPNeighbor)

	// lldpRemSysName - remote system name
	extractLLDPLabels(families, "lldpRemSysName", neighborMap, func(n *LLDPNeighbor, labels map[string]string) {
		n.RemoteSysName = labels["lldpRemSysName"]
	})

	// lldpRemPortId - remote port ID
	extractLLDPLabels(families, "lldpRemPortId", neighborMap, func(n *LLDPNeighbor, labels map[string]string) {
		n.RemotePortID = labels["lldpRemPortId"]
	})

	// lldpRemPortDesc - remote port description
	extractLLDPLabels(families, "lldpRemPortDesc", neighborMap, func(n *LLDPNeighbor, labels map[string]string) {
		n.RemotePortDesc = labels["lldpRemPortDesc"]
	})

	// lldpRemSysDesc - remote system description
	extractLLDPLabels(families, "lldpRemSysDesc", neighborMap, func(n *LLDPNeighbor, labels map[string]string) {
		n.RemoteSysDesc = labels["lldpRemSysDesc"]
	})

	// lldpLocPortId - local port ID (may also help identify the local port)
	extractLLDPLabels(families, "lldpLocPortId", neighborMap, func(n *LLDPNeighbor, labels map[string]string) {
		n.LocalPortID = labels["lldpLocPortId"]
	})

	// lldpLocPortDesc - local port description
	extractLLDPLabels(families, "lldpLocPortDesc", neighborMap, func(n *LLDPNeighbor, labels map[string]string) {
		n.LocalPortDesc = labels["lldpLocPortDesc"]
	})

	for _, n := range neighborMap {
		result.Neighbors = append(result.Neighbors, *n)
	}

	// Extract interface information from IF-MIB
	ifMap := make(map[string]*InterfaceInfo)

	// ifDescr
	if mf, ok := families["ifDescr"]; ok {
		for _, m := range mf.GetMetric() {
			labels := labelMap(m)
			idx := labels["ifIndex"]
			if idx == "" {
				continue
			}
			if _, ok := ifMap[idx]; !ok {
				ifMap[idx] = &InterfaceInfo{Index: idx}
			}
			ifMap[idx].Descr = labels["ifDescr"]
		}
	}

	// ifOperStatus - 1=up, 2=down
	if mf, ok := families["ifOperStatus"]; ok {
		for _, m := range mf.GetMetric() {
			labels := labelMap(m)
			idx := labels["ifIndex"]
			if idx == "" {
				continue
			}
			if _, ok := ifMap[idx]; !ok {
				ifMap[idx] = &InterfaceInfo{Index: idx}
			}
			val := metricValue(m)
			if val == 1 {
				ifMap[idx].OperStatus = "up"
			} else {
				ifMap[idx].OperStatus = "down"
			}
		}
	}

	// ifAlias
	if mf, ok := families["ifAlias"]; ok {
		for _, m := range mf.GetMetric() {
			labels := labelMap(m)
			idx := labels["ifIndex"]
			if idx == "" {
				continue
			}
			if _, ok := ifMap[idx]; !ok {
				ifMap[idx] = &InterfaceInfo{Index: idx}
			}
			ifMap[idx].Alias = labels["ifAlias"]
		}
	}

	for _, iface := range ifMap {
		result.Interfaces = append(result.Interfaces, *iface)
	}

	return result, nil
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
