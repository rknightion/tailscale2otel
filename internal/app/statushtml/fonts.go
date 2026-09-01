package statushtml

import "embed"

// fontFiles is the self-hosted console font bundle. The admin router serves
// only the fixed filenames accepted by Font so request paths never select an
// arbitrary embedded file.
//
//go:embed fonts/*.woff2
var fontFiles embed.FS

// Font returns one self-hosted console font by its public filename. The caller
// owns HTTP policy (authentication, content type and cache headers); this
// package owns only the embedded bytes used by all three console pages.
func Font(name string) ([]byte, bool) {
	switch name {
	case "hanken-grotesk-latin.woff2", "hanken-grotesk-latin-ext.woff2", "JetBrainsMono-Variable.woff2":
		data, err := fontFiles.ReadFile("fonts/" + name)
		return data, err == nil
	default:
		return nil, false
	}
}
