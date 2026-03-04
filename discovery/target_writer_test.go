package discovery

import (
	"testing"
)

func TestNormalizeHostname(t *testing.T) {
	tests := []struct {
		name     string
		hostname string
		domain   string
		want     string
	}{
		{"fqdn stripped", "dev01.int.persimmons.ai", "int.persimmons.ai", "dev01"},
		{"already short", "dev01", "int.persimmons.ai", "dev01"},
		{"empty domain", "dev01.int.persimmons.ai", "", "dev01.int.persimmons.ai"},
		{"empty hostname", "", "int.persimmons.ai", ""},
		{"both empty", "", "", ""},
		{"partial domain match", "dev01.other.domain", "int.persimmons.ai", "dev01.other.domain"},
		{"domain only no dot prefix", "int.persimmons.ai", "int.persimmons.ai", "int.persimmons.ai"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeHostname(tt.hostname, tt.domain)
			if got != tt.want {
				t.Errorf("normalizeHostname(%q, %q) = %q, want %q", tt.hostname, tt.domain, got, tt.want)
			}
		})
	}
}

func TestBuildHostMap(t *testing.T) {
	hosts := []HostConnection{
		{Hostname: "dev01.int.persimmons.ai", SwitchID: "sw-01", TopologySource: "lldp"},
		{Hostname: "dev02.int.persimmons.ai", SwitchID: "sw-02", TopologySource: "lldp"},
		{Hostname: "shorthost", SwitchID: "sw-01", TopologySource: "lldp"},
	}
	domain := "int.persimmons.ai"

	hostMap := buildHostMap(hosts, domain)

	// Full FQDN keys should exist
	if _, ok := hostMap["dev01.int.persimmons.ai"]; !ok {
		t.Error("expected FQDN key dev01.int.persimmons.ai")
	}
	if _, ok := hostMap["dev02.int.persimmons.ai"]; !ok {
		t.Error("expected FQDN key dev02.int.persimmons.ai")
	}

	// Short name keys should exist
	if _, ok := hostMap["dev01"]; !ok {
		t.Error("expected short key dev01")
	}
	if _, ok := hostMap["dev02"]; !ok {
		t.Error("expected short key dev02")
	}

	// Host without domain should only have one key
	if _, ok := hostMap["shorthost"]; !ok {
		t.Error("expected key shorthost")
	}

	// Verify host map points to correct entries
	if hostMap["dev01"].SwitchID != "sw-01" {
		t.Errorf("dev01 short key should point to sw-01, got %s", hostMap["dev01"].SwitchID)
	}
}

func TestBuildHostMap_ShortNameConflict(t *testing.T) {
	// Two hosts that normalize to the same short name — first wins
	hosts := []HostConnection{
		{Hostname: "dev01.int.persimmons.ai", SwitchID: "sw-01"},
		{Hostname: "dev01", SwitchID: "sw-02"},
	}

	hostMap := buildHostMap(hosts, "int.persimmons.ai")

	// FQDN key should point to first host
	if hostMap["dev01.int.persimmons.ai"].SwitchID != "sw-01" {
		t.Error("FQDN key should point to first host (sw-01)")
	}

	// Short name "dev01" — first host's normalizeHostname produces "dev01",
	// but the second host's full hostname IS "dev01", and it's inserted as a full key.
	// The short-name entry from the first host is inserted first.
	// Then the second host's full hostname "dev01" overwrites it.
	if hostMap["dev01"].SwitchID != "sw-02" {
		t.Errorf("short key dev01 should point to second host (sw-02) since it's the exact hostname, got %s", hostMap["dev01"].SwitchID)
	}
}

func TestApplyTopologyLabels(t *testing.T) {
	tg := &TargetGroup{
		Targets: []string{"host01:9100"},
		Labels:  make(map[string]string),
	}
	host := &HostConnection{
		SwitchID:        "sw-01",
		SwitchPort:      "port1",
		SwitchPortIndex: "GigabitEthernet0/1",
		UplinkSwitchL1:  "core-01",
		UplinkPortL1:    "port10",
		UplinkSwitchL2:  "gw-01",
		UplinkPortL2:    "port1",
		NetworkPath:     "host01→sw-01→core-01→gw-01",
		TopologySource:  "lldp",
	}

	applyTopologyLabels(tg, host, "lldp")

	checks := map[string]string{
		"switch_id":         "sw-01",
		"switch_port":       "port1",
		"switch_port_index": "GigabitEthernet0/1",
		"uplink_switch_l1":  "core-01",
		"uplink_port_l1":    "port10",
		"uplink_switch_l2":  "gw-01",
		"uplink_port_l2":    "port1",
		"network_path":      "host01→sw-01→core-01→gw-01",
		"topology_source":   "lldp",
	}
	for key, want := range checks {
		if got := tg.Labels[key]; got != want {
			t.Errorf("label %q = %q, want %q", key, got, want)
		}
	}
	if _, ok := tg.Labels["topology_updated"]; !ok {
		t.Error("expected topology_updated label to be set")
	}
}

func TestEnrichTargetGroup_DirectMatch(t *testing.T) {
	hostMap := map[string]*HostConnection{
		"host01": {
			Hostname:       "host01",
			SwitchID:       "sw-01",
			SwitchPort:     "port1",
			NetworkPath:    "host01→sw-01",
			TopologySource: "lldp",
		},
	}
	tg := &TargetGroup{
		Targets: []string{"host01:9100"},
		Labels:  map[string]string{"hostname": "host01"},
	}

	enrichTargetGroup(tg, hostMap, "int.persimmons.ai")

	if tg.Labels["topology_source"] != "lldp" {
		t.Errorf("expected topology_source=lldp, got %s", tg.Labels["topology_source"])
	}
	if tg.Labels["switch_id"] != "sw-01" {
		t.Errorf("expected switch_id=sw-01, got %s", tg.Labels["switch_id"])
	}
}

func TestEnrichTargetGroup_FQDNMatch(t *testing.T) {
	// LLDP reports FQDN, target file uses short name
	hostMap := map[string]*HostConnection{
		"fast-cad.int.persimmons.ai": {
			Hostname:       "fast-cad.int.persimmons.ai",
			SwitchID:       "sw-01",
			SwitchPort:     "port5",
			NetworkPath:    "fast-cad.int.persimmons.ai→sw-01",
			TopologySource: "lldp",
		},
		"fast-cad": { // short-name key from buildHostMap
			Hostname:       "fast-cad.int.persimmons.ai",
			SwitchID:       "sw-01",
			SwitchPort:     "port5",
			NetworkPath:    "fast-cad.int.persimmons.ai→sw-01",
			TopologySource: "lldp",
		},
	}
	tg := &TargetGroup{
		Targets: []string{"fast-cad:9100"},
		Labels:  map[string]string{"hostname": "fast-cad"},
	}

	enrichTargetGroup(tg, hostMap, "int.persimmons.ai")

	if tg.Labels["topology_source"] != "lldp" {
		t.Errorf("expected topology_source=lldp, got %s", tg.Labels["topology_source"])
	}
	if tg.Labels["switch_id"] != "sw-01" {
		t.Errorf("expected switch_id=sw-01, got %s", tg.Labels["switch_id"])
	}
}

func TestEnrichTargetGroup_ParentHost(t *testing.T) {
	hostMap := map[string]*HostConnection{
		"dev07": {
			Hostname:       "dev07",
			SwitchID:       "sw-02",
			SwitchPort:     "port3",
			NetworkPath:    "dev07→sw-02→gw-01",
			TopologySource: "lldp",
		},
	}
	tg := &TargetGroup{
		Targets: []string{"sim-dev-1001:9100"},
		Labels: map[string]string{
			"hostname":    "sim-dev-1001",
			"host_type":   "virtual",
			"parent_host": "dev07",
		},
	}

	enrichTargetGroup(tg, hostMap, "int.persimmons.ai")

	if tg.Labels["topology_source"] != "inherited:dev07" {
		t.Errorf("expected topology_source=inherited:dev07, got %s", tg.Labels["topology_source"])
	}
	if tg.Labels["switch_id"] != "sw-02" {
		t.Errorf("expected switch_id=sw-02, got %s", tg.Labels["switch_id"])
	}
	if tg.Labels["network_path"] != "vm→dev07→sw-02→gw-01" {
		t.Errorf("expected network_path=vm→dev07→sw-02→gw-01, got %s", tg.Labels["network_path"])
	}
}

func TestEnrichTargetGroup_ParentHostFQDN(t *testing.T) {
	// parent_host uses short name, LLDP discovered the parent with FQDN
	hostMap := map[string]*HostConnection{
		"dev07.int.persimmons.ai": {
			Hostname:       "dev07.int.persimmons.ai",
			SwitchID:       "sw-02",
			SwitchPort:     "port3",
			NetworkPath:    "dev07.int.persimmons.ai→sw-02→gw-01",
			TopologySource: "lldp",
		},
		"dev07": { // short-name key from buildHostMap
			Hostname:       "dev07.int.persimmons.ai",
			SwitchID:       "sw-02",
			SwitchPort:     "port3",
			NetworkPath:    "dev07.int.persimmons.ai→sw-02→gw-01",
			TopologySource: "lldp",
		},
	}
	tg := &TargetGroup{
		Targets: []string{"sim-dev-1001:9100"},
		Labels: map[string]string{
			"hostname":    "sim-dev-1001",
			"host_type":   "virtual",
			"parent_host": "dev07",
		},
	}

	enrichTargetGroup(tg, hostMap, "int.persimmons.ai")

	if tg.Labels["topology_source"] != "inherited:dev07.int.persimmons.ai" {
		t.Errorf("expected topology_source=inherited:dev07.int.persimmons.ai, got %s", tg.Labels["topology_source"])
	}
	if tg.Labels["switch_id"] != "sw-02" {
		t.Errorf("expected switch_id=sw-02, got %s", tg.Labels["switch_id"])
	}
	if tg.Labels["network_path"] != "vm→dev07.int.persimmons.ai→sw-02→gw-01" {
		t.Errorf("expected network_path=vm→dev07.int.persimmons.ai→sw-02→gw-01, got %s", tg.Labels["network_path"])
	}
}

func TestEnrichTargetGroup_NoMatch(t *testing.T) {
	hostMap := map[string]*HostConnection{
		"host01": {Hostname: "host01", SwitchID: "sw-01", TopologySource: "lldp"},
	}
	tg := &TargetGroup{
		Targets: []string{"unknown-host:9100"},
		Labels:  map[string]string{"hostname": "unknown-host"},
	}

	enrichTargetGroup(tg, hostMap, "int.persimmons.ai")

	if tg.Labels["topology_source"] != "unknown" {
		t.Errorf("expected topology_source=unknown, got %s", tg.Labels["topology_source"])
	}
	if tg.Labels["switch_id"] != "unknown" {
		t.Errorf("expected switch_id=unknown, got %s", tg.Labels["switch_id"])
	}
}

func TestEnrichTargetGroup_NilLabels(t *testing.T) {
	hostMap := map[string]*HostConnection{
		"host01": {Hostname: "host01", SwitchID: "sw-01", TopologySource: "lldp"},
	}
	tg := &TargetGroup{
		Targets: []string{"host01:9100"},
	}

	enrichTargetGroup(tg, hostMap, "")

	if tg.Labels == nil {
		t.Fatal("expected labels to be initialized")
	}
	if tg.Labels["switch_id"] != "sw-01" {
		t.Errorf("expected switch_id=sw-01, got %s", tg.Labels["switch_id"])
	}
}
