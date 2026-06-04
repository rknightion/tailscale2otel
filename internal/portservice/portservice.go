// Package portservice maps a transport protocol and port number to the IANA
// service name registered for it (e.g. tcp/443 -> "https", tcp/22 -> "ssh").
//
// The data is a trimmed copy of the IANA "Service Name and Transport Protocol
// Port Number Registry", embedded at build time so the lookup works in a
// scratch/distroless container with no /etc/services (which net.LookupPort
// depends on). Regenerate it with `go generate ./internal/portservice` (see
// gen.go). It is the same idea as flowlog.protoNames, scaled to the registry.
package portservice

import (
	"encoding/csv"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"

	_ "embed"
)

//go:generate go run gen.go

//go:embed service-names-port-numbers.csv
var ianaCSV string

// key identifies one registry assignment: a transport name and port number.
type key struct {
	transport string
	port      uint16
}

var (
	once  sync.Once
	table map[key]string
)

// load parses the embedded CSV into table on first use. Rows whose port does
// not parse (including the header) or whose service name is empty are skipped,
// mirroring the trimming gen.go applies, so a stray malformed row is ignored
// rather than fatal.
func load() {
	table = make(map[key]string, 12000)
	r := csv.NewReader(strings.NewReader(ianaCSV))
	r.FieldsPerRecord = -1
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || len(rec) < 3 {
			continue
		}
		service, portStr, transport := rec[0], rec[1], rec[2]
		if service == "" || transport == "" {
			continue
		}
		p, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil {
			continue
		}
		k := key{transport: transport, port: uint16(p)}
		if _, ok := table[k]; !ok {
			table[k] = service
		}
	}
}

// LookupName returns the IANA service name registered for the given transport
// ("tcp", "udp", "sctp") and port, and whether one was found. Unknown
// transport/port combinations return ("", false).
func LookupName(transport string, port uint16) (string, bool) {
	once.Do(load)
	name, ok := table[key{transport: transport, port: port}]
	return name, ok
}
