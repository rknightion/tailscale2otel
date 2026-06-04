//go:build ignore

// Command gen produces service-names-port-numbers.csv: a trimmed copy of the
// IANA "Service Name and Transport Protocol Port Number Registry" holding only
// the three columns this package needs (service name, port, transport) for the
// tcp/udp/sctp rows that carry a service name and a single numeric port.
//
// Refresh the embedded data with:
//
//	go run gen.go               # download the current registry from IANA
//	go run gen.go -raw FILE     # trim a previously downloaded raw CSV (offline)
//
// The IANA registry changes infrequently; there is no CI gate on freshness.
package main

import (
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
)

const ianaURL = "https://www.iana.org/assignments/service-names-port-numbers/service-names-port-numbers.csv"

// transports we keep: connection-oriented/datagram protocols that carry ports
// and appear in Tailscale flow logs.
var keepTransport = map[string]bool{"tcp": true, "udp": true, "sctp": true}

type row struct {
	service   string
	port      uint16
	transport string
}

func main() {
	raw := flag.String("raw", "", "path to a downloaded raw IANA CSV (default: download from IANA)")
	out := flag.String("out", "service-names-port-numbers.csv", "output path for the trimmed CSV")
	flag.Parse()

	var src io.ReadCloser
	if *raw != "" {
		f, err := os.Open(*raw)
		if err != nil {
			log.Fatalf("open raw CSV: %v", err)
		}
		src = f
	} else {
		resp, err := http.Get(ianaURL) //nolint:gosec // fixed, trusted IANA URL
		if err != nil {
			log.Fatalf("download IANA CSV: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			log.Fatalf("download IANA CSV: HTTP %d", resp.StatusCode)
		}
		src = resp.Body
	}
	defer src.Close()

	r := csv.NewReader(src)
	r.FieldsPerRecord = -1 // tolerate the registry's varying trailing columns
	r.LazyQuotes = true

	rows, seen := []row{}, map[string]bool{}
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			log.Fatalf("read CSV: %v", err)
		}
		if len(rec) < 3 {
			continue
		}
		service, portStr, transport := rec[0], rec[1], rec[2]
		if service == "" || portStr == "" || strings.Contains(portStr, "-") {
			continue // service-name-only, unnamed, or a port range
		}
		if !keepTransport[transport] {
			continue
		}
		p, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil {
			continue // header row ("Port Number") and any non-numeric port
		}
		k := transport + "/" + portStr
		if seen[k] {
			continue // first IANA assignment wins
		}
		seen[k] = true
		rows = append(rows, row{service: service, port: uint16(p), transport: transport})
	}

	// Deterministic output: transport, then port.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].transport != rows[j].transport {
			return rows[i].transport < rows[j].transport
		}
		return rows[i].port < rows[j].port
	})

	f, err := os.Create(*out)
	if err != nil {
		log.Fatalf("create %s: %v", *out, err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if err := w.Write([]string{"service", "port", "transport"}); err != nil {
		log.Fatalf("write header: %v", err)
	}
	for _, rw := range rows {
		if err := w.Write([]string{rw.service, strconv.Itoa(int(rw.port)), rw.transport}); err != nil {
			log.Fatalf("write row: %v", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		log.Fatalf("flush: %v", err)
	}
	fmt.Printf("wrote %d rows to %s\n", len(rows), *out)
}
