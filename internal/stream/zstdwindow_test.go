package stream

import "testing"

// TestZstdMaxWindow pins the decoder back-reference window cap derived from the
// decompressed-body limit: a frame must not be allowed to declare a window larger
// than the most we would ever decompress (the library default is 512 MB).
func TestZstdMaxWindow(t *testing.T) {
	cases := []struct {
		name    string
		maxBody int64
		want    uint64
	}{
		{"default 64MiB caps the window at the body limit", 64 << 20, 64 << 20},
		{"uncapped body keeps the library default (0 = unset)", -1, 0},
		{"zero body limit means unset", 0, 0},
		{"tiny limit clamps up to zstd's 1KB minimum", 500, 1 << 10},
	}
	for _, tc := range cases {
		if got := zstdMaxWindow(tc.maxBody); got != tc.want {
			t.Errorf("%s: zstdMaxWindow(%d) = %d, want %d", tc.name, tc.maxBody, got, tc.want)
		}
	}
}
