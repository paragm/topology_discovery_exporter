package discovery

import (
	"os"
	"sort"
	"testing"
)

func TestParseSNMPResponse_NeighborCount(t *testing.T) {
	body := readTestData(t)
	result, err := ParseSNMPResponse(body, "test-switch")
	if err != nil {
		t.Fatalf("ParseSNMPResponse: %v", err)
	}
	if len(result.Neighbors) != 3 {
		t.Errorf("expected 3 neighbors, got %d", len(result.Neighbors))
	}
}

func TestParseSNMPResponse_LocalPortJoin(t *testing.T) {
	body := readTestData(t)
	result, err := ParseSNMPResponse(body, "test-switch")
	if err != nil {
		t.Fatalf("ParseSNMPResponse: %v", err)
	}

	// Sort by RemoteSysName for deterministic assertions.
	sort.Slice(result.Neighbors, func(i, j int) bool {
		return result.Neighbors[i].RemoteSysName < result.Neighbors[j].RemoteSysName
	})

	tests := []struct {
		remoteSysName     string
		wantLocalPortID   string
		wantLocalPortDesc string
	}{
		{"host01.example.net", "gi0/1", "GigabitEthernet0/1"},
		{"host02.example.net", "gi0/2", "GigabitEthernet0/2"},
		{"test-core-sw01", "gi0/24", "GigabitEthernet0/24"},
	}

	for i, tt := range tests {
		n := result.Neighbors[i]
		if n.RemoteSysName != tt.remoteSysName {
			t.Errorf("neighbor[%d]: expected RemoteSysName=%q, got %q", i, tt.remoteSysName, n.RemoteSysName)
			continue
		}
		if n.LocalPortID != tt.wantLocalPortID {
			t.Errorf("%s: LocalPortID = %q, want %q", tt.remoteSysName, n.LocalPortID, tt.wantLocalPortID)
		}
		if n.LocalPortDesc != tt.wantLocalPortDesc {
			t.Errorf("%s: LocalPortDesc = %q, want %q", tt.remoteSysName, n.LocalPortDesc, tt.wantLocalPortDesc)
		}
	}
}

func TestParseSNMPResponse_RemoteFields(t *testing.T) {
	body := readTestData(t)
	result, err := ParseSNMPResponse(body, "test-switch")
	if err != nil {
		t.Fatalf("ParseSNMPResponse: %v", err)
	}

	byName := make(map[string]LLDPNeighbor)
	for _, n := range result.Neighbors {
		byName[n.RemoteSysName] = n
	}

	n, ok := byName["host01.example.net"]
	if !ok {
		t.Fatal("host01.example.net not found in neighbors")
	}
	if n.RemotePortID != "eth0" {
		t.Errorf("host01 RemotePortID = %q, want %q", n.RemotePortID, "eth0")
	}
	if n.RemotePortDesc != "Network interface eth0" {
		t.Errorf("host01 RemotePortDesc = %q, want %q", n.RemotePortDesc, "Network interface eth0")
	}
}

func TestParseSNMPResponse_Interfaces(t *testing.T) {
	body := readTestData(t)
	result, err := ParseSNMPResponse(body, "test-switch")
	if err != nil {
		t.Fatalf("ParseSNMPResponse: %v", err)
	}

	if len(result.Interfaces) != 3 {
		t.Errorf("expected 3 interfaces, got %d", len(result.Interfaces))
	}

	byIndex := make(map[string]InterfaceInfo)
	for _, iface := range result.Interfaces {
		byIndex[iface.Index] = iface
	}

	iface, ok := byIndex["1"]
	if !ok {
		t.Fatal("interface index 1 not found")
	}
	if iface.Descr != "GigabitEthernet0/1" {
		t.Errorf("interface 1 Descr = %q, want %q", iface.Descr, "GigabitEthernet0/1")
	}
	if iface.OperStatus != "up" {
		t.Errorf("interface 1 OperStatus = %q, want %q", iface.OperStatus, "up")
	}
}

func TestParseSNMPResponse_SwitchName(t *testing.T) {
	body := readTestData(t)
	result, err := ParseSNMPResponse(body, "my-switch")
	if err != nil {
		t.Fatalf("ParseSNMPResponse: %v", err)
	}
	if result.SysName != "my-switch" {
		t.Errorf("SysName = %q, want %q", result.SysName, "my-switch")
	}
}

func TestParseSNMPResponse_MissingLocalPort(t *testing.T) {
	// Simulate the old scenario: remote metrics present but no lldpLocPort* metrics.
	data := `# HELP lldpRemSysName Remote system name.
# TYPE lldpRemSysName gauge
lldpRemSysName{lldpRemTimeMark="0",lldpRemLocalPortNum="5",lldpRemIndex="1",lldpRemSysName="orphan-host"} 1
`
	result, err := ParseSNMPResponse([]byte(data), "sw1")
	if err != nil {
		t.Fatalf("ParseSNMPResponse: %v", err)
	}
	if len(result.Neighbors) != 1 {
		t.Fatalf("expected 1 neighbor, got %d", len(result.Neighbors))
	}
	n := result.Neighbors[0]
	if n.RemoteSysName != "orphan-host" {
		t.Errorf("RemoteSysName = %q, want %q", n.RemoteSysName, "orphan-host")
	}
	// LocalPort fields should be empty when no local port metrics exist.
	if n.LocalPortID != "" {
		t.Errorf("LocalPortID = %q, want empty", n.LocalPortID)
	}
	if n.LocalPortDesc != "" {
		t.Errorf("LocalPortDesc = %q, want empty", n.LocalPortDesc)
	}
}

func TestParseSNMPResponse_MultipleNeighborsOnSamePort(t *testing.T) {
	// Two neighbors on the same local port (different lldpRemIndex).
	data := `# HELP lldpRemSysName Remote system name.
# TYPE lldpRemSysName gauge
lldpRemSysName{lldpRemTimeMark="0",lldpRemLocalPortNum="9",lldpRemIndex="1",lldpRemSysName="host-a"} 1
lldpRemSysName{lldpRemTimeMark="0",lldpRemLocalPortNum="9",lldpRemIndex="2",lldpRemSysName="host-b"} 1
# HELP lldpLocPortId Local port identifier.
# TYPE lldpLocPortId gauge
lldpLocPortId{lldpLocPortNum="9",lldpLocPortId="te0/9"} 1
# HELP lldpLocPortDesc Local port description.
# TYPE lldpLocPortDesc gauge
lldpLocPortDesc{lldpLocPortNum="9",lldpLocPortDesc="TenGigabitEthernet0/9"} 1
`
	result, err := ParseSNMPResponse([]byte(data), "sw1")
	if err != nil {
		t.Fatalf("ParseSNMPResponse: %v", err)
	}
	if len(result.Neighbors) != 2 {
		t.Fatalf("expected 2 neighbors, got %d", len(result.Neighbors))
	}
	// Both neighbors should get the same local port data.
	for _, n := range result.Neighbors {
		if n.LocalPortID != "te0/9" {
			t.Errorf("%s: LocalPortID = %q, want %q", n.RemoteSysName, n.LocalPortID, "te0/9")
		}
		if n.LocalPortDesc != "TenGigabitEthernet0/9" {
			t.Errorf("%s: LocalPortDesc = %q, want %q", n.RemoteSysName, n.LocalPortDesc, "TenGigabitEthernet0/9")
		}
	}
}

func TestExtractLocalPortMap(t *testing.T) {
	data := `# HELP lldpLocPortId Local port identifier.
# TYPE lldpLocPortId gauge
lldpLocPortId{lldpLocPortNum="1",lldpLocPortId="gi0/1"} 1
lldpLocPortId{lldpLocPortNum="2",lldpLocPortId="gi0/2"} 1
`
	result, err := ParseSNMPResponse([]byte(data), "sw1")
	if err != nil {
		t.Fatalf("ParseSNMPResponse: %v", err)
	}
	// No remote neighbors, so Neighbors should be empty.
	if len(result.Neighbors) != 0 {
		t.Errorf("expected 0 neighbors, got %d", len(result.Neighbors))
	}
}

func TestNeighborKey(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{
			name:   "all present",
			labels: map[string]string{"lldpRemTimeMark": "0", "lldpRemLocalPortNum": "1", "lldpRemIndex": "1"},
			want:   "0|1|1",
		},
		{
			name:   "all empty",
			labels: map[string]string{},
			want:   "",
		},
		{
			name:   "local port labels only",
			labels: map[string]string{"lldpLocPortNum": "5"},
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := neighborKey(tt.labels)
			if got != tt.want {
				t.Errorf("neighborKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func readTestData(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../testdata/snmp_response.txt")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	return data
}
