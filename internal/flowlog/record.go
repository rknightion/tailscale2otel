// Package flowlog defines the Tailscale network flow log record types and (in
// processor.go) the conversion to OTEL metrics and logs. Both the polling
// collector and the streaming receiver decode into these types and feed the
// same processor.
package flowlog

import "time"

// NetworkResponse is the GET /tailnet/{tailnet}/logging/network response body.
type NetworkResponse struct {
	Logs []FlowLog `json:"logs"`
}

// FlowLog is one node's flow record for a capture window.
type FlowLog struct {
	Logged          time.Time          `json:"logged"`
	NodeID          string             `json:"nodeId"`
	Start           time.Time          `json:"start"`
	End             time.Time          `json:"end"`
	VirtualTraffic  []ConnectionCounts `json:"virtualTraffic"`
	SubnetTraffic   []ConnectionCounts `json:"subnetTraffic"`
	ExitTraffic     []ConnectionCounts `json:"exitTraffic"`
	PhysicalTraffic []ConnectionCounts `json:"physicalTraffic"`
}

// ConnectionCounts is bidirectional traffic for a single 5-tuple in a window.
// Src and Dst are "addr:port" strings.
type ConnectionCounts struct {
	Proto   string `json:"proto"`
	Src     string `json:"src"`
	Dst     string `json:"dst"`
	TxPkts  int64  `json:"txPkts"`
	TxBytes int64  `json:"txBytes"`
	RxPkts  int64  `json:"rxPkts"`
	RxBytes int64  `json:"rxBytes"`
}
