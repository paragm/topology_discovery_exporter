package discovery

import (
	"log/slog"
	"sort"
	"strings"
)

// Switch represents a discovered network switch.
type Switch struct {
	Name     string `json:"name"`
	IP       string `json:"ip"`
	Role     string `json:"role"`
	SysDesc  string `json:"sys_desc"`
	UplinkTo string `json:"uplink_to"`
}

// HostConnection represents a host's connection to the network topology.
type HostConnection struct {
	Hostname        string `json:"hostname"`
	SwitchID        string `json:"switch_id"`
	SwitchPort      string `json:"switch_port"`
	SwitchPortIndex string `json:"switch_port_index"`
	UplinkSwitchL1  string `json:"uplink_switch_l1"`
	UplinkPortL1    string `json:"uplink_port_l1"`
	UplinkSwitchL2  string `json:"uplink_switch_l2"`
	UplinkPortL2    string `json:"uplink_port_l2"`
	NetworkPath     string `json:"network_path"`
	TopologySource  string `json:"topology_source"`
}

// Link represents a connection between two entities (switch-to-switch or switch-to-host).
type Link struct {
	From         string `json:"from"`
	FromPort     string `json:"from_port"`
	FromPortDesc string `json:"from_port_desc"`
	To           string `json:"to"`
	ToPort       string `json:"to_port"`
	LinkType     string `json:"link_type"`
	OperStatus   string `json:"oper_status"`
}

// roleHierarchy defines the priority of switch roles (higher = more upstream).
var roleHierarchy = map[string]int{
	"gateway":      4,
	"core":         3,
	"distribution": 2,
	"access":       1,
}

// BuildGraph constructs the topology graph from LLDP discovery results.
func BuildGraph(results []switchResult, knownSwitches map[string]SwitchConfig, logger *slog.Logger) ([]Switch, []HostConnection, []Link) {
	var switches []Switch
	var hosts []HostConnection
	var links []Link

	// Build switch info from discovered data
	switchByName := make(map[string]*Switch)
	for _, r := range results {
		sw := Switch{
			Name:    r.switchCfg.Name,
			IP:      r.switchCfg.Address,
			Role:    r.switchCfg.Role,
			SysDesc: r.lldp.SysDesc,
		}
		switchByName[r.switchCfg.Name] = &sw
	}

	// Also add switches from config that may not have responded
	for _, sc := range knownSwitches {
		if _, ok := switchByName[sc.Name]; !ok {
			switchByName[sc.Name] = &Switch{
				Name: sc.Name,
				IP:   sc.Address,
				Role: sc.Role,
			}
		}
	}

	// Process LLDP neighbors to identify links
	// Track hosts that have multiple connections (multi-NIC)
	hostConnections := make(map[string][]HostConnection)

	for _, r := range results {
		localSwitch := r.switchCfg.Name
		localRole := r.switchCfg.Role

		for _, neighbor := range r.lldp.Neighbors {
			remoteName := neighbor.RemoteSysName

			// Check if the remote is a known switch
			if remoteCfg, isSwitch := knownSwitches[remoteName]; isSwitch {
				// Switch-to-switch link
				linkType := classifyLink(localRole, remoteCfg.Role)
				links = append(links, Link{
					From:         localSwitch,
					FromPort:     neighbor.LocalPortID,
					FromPortDesc: neighbor.LocalPortDesc,
					To:           remoteName,
					ToPort:       neighbor.RemotePortID,
					LinkType:     linkType,
					OperStatus:   "up", // LLDP neighbor exists → port is up
				})

				// Determine uplink: lower-role switch uplinks to higher-role switch
				if roleHierarchy[localRole] < roleHierarchy[remoteCfg.Role] {
					if sw, ok := switchByName[localSwitch]; ok {
						sw.UplinkTo = remoteName
					}
				}
			} else if remoteName != "" {
				// Host connection
				conn := HostConnection{
					Hostname:        remoteName,
					SwitchID:        localSwitch,
					SwitchPort:      neighbor.LocalPortID,
					SwitchPortIndex: neighbor.LocalPortDesc,
					TopologySource:  "lldp",
				}
				hostConnections[remoteName] = append(hostConnections[remoteName], conn)

				links = append(links, Link{
					From:         localSwitch,
					FromPort:     neighbor.LocalPortID,
					FromPortDesc: neighbor.LocalPortDesc,
					To:           remoteName,
					ToPort:       neighbor.RemotePortID,
					LinkType:     "host",
					OperStatus:   "up",
				})
			}
		}

		// Add interface status links for ports without LLDP neighbors
		for _, iface := range r.lldp.Interfaces {
			if !hasLLDPNeighbor(r.lldp.Neighbors, iface.Descr) {
				links = append(links, Link{
					From:         localSwitch,
					FromPort:     iface.Index,
					FromPortDesc: iface.Descr,
					To:           "",
					ToPort:       "",
					LinkType:     "unused",
					OperStatus:   iface.OperStatus,
				})
			}
		}
	}

	// Resolve multi-NIC hosts: pick primary by lowest port index
	for hostname, conns := range hostConnections {
		primary := pickPrimaryConnection(conns)

		// Enrich with uplink path
		primary = enrichUplinkPath(primary, switchByName)

		hosts = append(hosts, primary)

		if len(conns) > 1 {
			logger.Info("multi-NIC host detected, using primary connection",
				"host", hostname, "connections", len(conns),
				"primary_switch", primary.SwitchID, "primary_port", primary.SwitchPort)
		}
	}

	// Sort hosts for deterministic output
	sort.Slice(hosts, func(i, j int) bool {
		return hosts[i].Hostname < hosts[j].Hostname
	})

	// Collect switches
	for _, sw := range switchByName {
		switches = append(switches, *sw)
	}
	sort.Slice(switches, func(i, j int) bool {
		return switches[i].Name < switches[j].Name
	})

	return switches, hosts, links
}

// classifyLink determines the link type based on the roles of connected switches.
func classifyLink(localRole, remoteRole string) string {
	if localRole == remoteRole {
		return "inter-switch"
	}
	return "uplink"
}

// pickPrimaryConnection selects the primary connection for a multi-NIC host.
// Preference: lowest port index.
func pickPrimaryConnection(conns []HostConnection) HostConnection {
	if len(conns) == 1 {
		return conns[0]
	}

	sort.Slice(conns, func(i, j int) bool {
		return conns[i].SwitchPortIndex < conns[j].SwitchPortIndex
	})
	return conns[0]
}

// enrichUplinkPath adds uplink switch info and network path to a host connection.
func enrichUplinkPath(conn HostConnection, switchByName map[string]*Switch) HostConnection { //nolint:gocritic // value copy intentional, returns modified copy
	sw, ok := switchByName[conn.SwitchID]
	if !ok {
		return conn
	}

	// L1 uplink: direct upstream switch
	if sw.UplinkTo != "" {
		conn.UplinkSwitchL1 = sw.UplinkTo

		// Find the uplink port (from the links we already know)
		// For now, set it to the switch name — specific port requires reverse lookup
		uplinkSw, ok := switchByName[sw.UplinkTo]
		if ok && uplinkSw.UplinkTo != "" {
			conn.UplinkSwitchL2 = uplinkSw.UplinkTo
		}
	}

	// Build network path: host → access → core → gateway
	conn.NetworkPath = buildNetworkPath(conn.Hostname, conn.SwitchID, switchByName)

	return conn
}

// buildNetworkPath traces the uplink chain from host to the top of the hierarchy.
func buildNetworkPath(hostname, switchID string, switchByName map[string]*Switch) string {
	parts := []string{hostname}
	visited := make(map[string]bool)
	current := switchID

	for current != "" && !visited[current] {
		visited[current] = true
		parts = append(parts, current)
		sw, ok := switchByName[current]
		if !ok {
			break
		}
		current = sw.UplinkTo
	}

	return strings.Join(parts, "→")
}

// hasLLDPNeighbor checks if any LLDP neighbor uses the given port.
func hasLLDPNeighbor(neighbors []LLDPNeighbor, portDesc string) bool {
	for _, n := range neighbors {
		if n.LocalPortDesc == portDesc || n.LocalPortID == portDesc {
			return true
		}
	}
	return false
}
