package flowlog

import (
	"strconv"
	"testing"
)

func TestNodeRefIndex(t *testing.T) {
	for _, large := range []bool{false, true} {
		t.Run(strconv.FormatBool(large), func(t *testing.T) {
			f := FlowLog{SrcNode: &NodeRef{Addresses: []string{"100.64.0.1", "invalid"}}, DstNodes: []NodeRef{
				{Addresses: []string{"100.64.0.1", "100.64.0.2", "fd7a:115c:a1e0::2"}},
				{Addresses: []string{"100.64.0.2"}},
			}}
			if large {
				for i := 0; i < 40; i++ {
					f.DstNodes = append(f.DstNodes, NodeRef{Addresses: []string{"fd7a:115c:a1e0::" + strconv.FormatInt(int64(i+100), 16)}})
				}
			}
			ix := f.refIndex()
			for _, tc := range []struct {
				addr string
				want *NodeRef
			}{
				{"100.64.0.1", f.SrcNode}, {"100.64.0.2", &f.DstNodes[0]}, {"fd7a:115c:a1e0::2", &f.DstNodes[0]},
				{"", nil}, {"invalid", nil}, {"192.0.2.2", nil},
			} {
				if got := ix.lookup(tc.addr); got != tc.want {
					t.Errorf("lookup %q = %p, want %p", tc.addr, got, tc.want)
				}
			}
		})
	}
	if (FlowLog{}).refIndex().lookup("100.64.0.1") != nil {
		t.Fatal("empty record returned identity")
	}
}
