package discovery

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

var snmpHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

// QuerySNMPExporter queries the SNMP exporter's HTTP API for LLDP metrics.
// It returns the raw Prometheus text format response body.
func QuerySNMPExporter(ctx context.Context, snmpExporterURL, target, module, auth string) ([]byte, error) {
	u, err := url.Parse(snmpExporterURL)
	if err != nil {
		return nil, fmt.Errorf("parse SNMP exporter URL: %w", err)
	}
	u.Path = "/snmp"

	q := u.Query()
	q.Set("target", target)
	q.Set("module", module)
	q.Set("auth", auth)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := snmpHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request to SNMP exporter: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 50<<20)) // 50 MB max
		return nil, fmt.Errorf("SNMP exporter returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 50<<20)) // 50 MB max
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	return body, nil
}
