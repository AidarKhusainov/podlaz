package daemon

import (
	"net"
	"strconv"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

const (
	tunTransactionLabelServerEndpoint = "diagnostic_server_endpoint"
	tunTransactionLabelServerName     = "diagnostic_server_name"
)

func tunTransactionDiagnosticLabels(p profile.Profile) map[string]string {
	server := strings.TrimSpace(p.Server)
	serverName := strings.TrimSpace(p.ServerName)
	if serverName == "" {
		serverName = server
	}
	labels := map[string]string{}
	if server != "" && p.Port != 0 {
		labels[tunTransactionLabelServerEndpoint] = net.JoinHostPort(server, strconv.Itoa(int(p.Port)))
	}
	if serverName != "" {
		labels[tunTransactionLabelServerName] = serverName
	}
	if len(labels) == 0 {
		return nil
	}
	return labels
}

func tunDiagnosticServerMetadata(tx txstate.Transaction) (endpoint, serverName string) {
	if tx.Labels == nil {
		return "", ""
	}
	return strings.TrimSpace(tx.Labels[tunTransactionLabelServerEndpoint]), strings.TrimSpace(tx.Labels[tunTransactionLabelServerName])
}

func tunDiagnosticHostname(endpoint string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(endpoint))
	if err == nil {
		return host
	}
	return strings.Trim(strings.TrimSpace(endpoint), "[]")
}
