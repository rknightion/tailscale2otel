package stream

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http/httptest"
	"testing"
)

func TestStreamTokenCompareUsesSharedDigest(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "stream.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	shared := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if pkg.Name == "subtle" && sel.Sel.Name == "ConstantTimeCompare" {
			t.Error("receiver compares variable-length credentials directly")
		}
		if pkg.Name == "httpguard" && sel.Sel.Name == "TokenEqual" {
			shared++
		}
		return true
	})
	if shared != 2 {
		t.Errorf("shared digest comparisons=%d, want Basic and Splunk", shared)
	}
}

func TestStreamTokenCompareCredentials(t *testing.T) {
	s := &Server{}
	for _, scheme := range []string{"Basic", "Splunk"} {
		for _, candidate := range []string{"token", "wrong", "longer-token", ""} {
			r := httptest.NewRequest("POST", "/", nil)
			if scheme == "Basic" {
				r.SetBasicAuth("any-user", candidate)
			} else {
				r.Header.Set("Authorization", scheme+" "+candidate)
			}
			if got := s.authorizedWithToken(r, "token"); got != (candidate == "token") {
				t.Errorf("%s candidate %q accepted=%v", scheme, candidate, got)
			}
			if !s.authorizedWithToken(r, "") {
				t.Error("empty configured token changed behavior")
			}
		}
	}
}
