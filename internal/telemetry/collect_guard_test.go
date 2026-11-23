package telemetry

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

const (
	collectGuardEmitter   = "internal/telemetry/emitter.go"
	collectGuardProvider  = "internal/telemetry/provider.go"
	collectGuardScheduled = "internal/telemetry/scheduled_metrics.go"
)

// collectGuardScan reports every production construct that could make a
// scheduled Collect fail after delta aggregation has reset. Names are matched
// wherever they appear, so an alias, a method value or a dot import cannot hide
// one. allowed counts the three known-safe sites, so a scan that stopped
// seeing real source fails instead of passing empty.
func collectGuardScan(fset *token.FileSet, rel string, file *ast.File) (violations []string, allowed int) {
	var stack []ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		safe := false
		switch id.Name {
		case "RegisterCallback", "WithInt64Callback", "SetMeterProvider", "WithMeterProvider":
		case "WithProducer":
			safe = rel == collectGuardScheduled && collectGuardFunc(stack) == "newScheduledMetricReader"
		case "MetricProducers":
			safe = collectGuardProducerField(rel, stack)
		case "WithFloat64Callback":
			safe = rel == collectGuardEmitter && collectGuardFunc(stack) == "GaugeSnapshot" && collectGuardCallbackNeverFails(stack)
		default:
			return true
		}
		if safe {
			allowed++
		} else {
			violations = append(violations, fmt.Sprintf("%s:%d: %s can fail Collect after reset", rel, fset.Position(id.Pos()).Line, id.Name))
		}
		return true
	})
	return violations, allowed
}

func collectGuardFunc(stack []ast.Node) string {
	for i := len(stack) - 1; i >= 0; i-- {
		if decl, ok := stack[i].(*ast.FuncDecl); ok {
			return decl.Name.Name
		}
	}
	return ""
}

// The field declaration and the one range that forwards it are the only uses;
// any assignment or composite-literal key would configure a real Producer.
func collectGuardProducerField(rel string, stack []ast.Node) bool {
	if len(stack) < 3 {
		return false
	}
	switch rel {
	case collectGuardProvider:
		_, field := stack[len(stack)-2].(*ast.Field)
		return field && collectGuardFunc(stack) == ""
	case collectGuardScheduled:
		selector, ok := stack[len(stack)-2].(*ast.SelectorExpr)
		if !ok || collectGuardFunc(stack) != "newScheduledMetricReader" {
			return false
		}
		loop, ok := stack[len(stack)-3].(*ast.RangeStmt)
		return ok && loop.X == selector
	}
	return false
}

// collectGuardCallbackNeverFails accepts only a function literal whose every
// return, ignoring nested functions, is the literal nil.
func collectGuardCallbackNeverFails(stack []ast.Node) bool {
	if len(stack) < 3 {
		return false
	}
	call, ok := stack[len(stack)-3].(*ast.CallExpr)
	if !ok || len(call.Args) != 1 || call.Fun != stack[len(stack)-2] {
		return false
	}
	callback, ok := call.Args[0].(*ast.FuncLit)
	if !ok {
		return false
	}
	returns, clean := 0, true
	ast.Inspect(callback.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			returns++
			if len(node.Results) != 1 {
				clean = false
				break
			}
			result, ok := node.Results[0].(*ast.Ident)
			clean = clean && ok && result.Name == "nil"
		}
		return true
	})
	return clean && returns > 0
}

// A production callback or Producer that can return an error would let a delta
// Collect fail after reset. The scheduled reader then poisons the required work
// rather than covering it, so adding one needs a deliberate design decision.
func TestCollectGuard_ProductionCannotFailCollectAfterReset(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var violations []string
	allowed, files := 0, 0
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			files++
			found, safe := collectGuardScan(fset, filepath.ToSlash(rel), file)
			violations = append(violations, found...)
			allowed += safe
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(violations) != 0 {
		t.Fatalf("production source can fail Collect after reset:\n%s", strings.Join(violations, "\n"))
	}
	// One callback, one WithProducer, the field and the range that reads it.
	if files == 0 || allowed != 4 {
		t.Fatalf("guard scanned %d files and saw %d known-safe sites, want 4", files, allowed)
	}
}

func TestCollectGuard_NegativeSources(t *testing.T) {
	const safeCallback = "package p\nfunc (e *x) GaugeSnapshot() { m(metric.WithFloat64Callback(func(c C, o O) error { o.Observe(1); return nil })) }\n"
	for name, tc := range map[string]struct {
		rel, source string
		wantBad     bool
	}{
		"safe callback":             {collectGuardEmitter, safeCallback, false},
		"callback returns an error": {collectGuardEmitter, strings.Replace(safeCallback, "return nil", "return errBad", 1), true},
		"callback may return error": {collectGuardEmitter, strings.Replace(safeCallback, "o.Observe(1);", "if bad { return errBad };", 1), true},
		"callback passed by name":   {collectGuardEmitter, "package p\nfunc (e *x) GaugeSnapshot() { m(metric.WithFloat64Callback(cb)) }\n", true},
		"callback in another file":  {"internal/app/app.go", safeCallback, true},
		"callback in another func":  {collectGuardEmitter, strings.Replace(safeCallback, "GaugeSnapshot", "Gauge", 1), true},
		"int64 callback":            {"internal/app/app.go", "package p\nvar _ = metric.WithInt64Callback(nil)\n", true},
		"multi callback":            {"internal/app/app.go", "package p\nfunc f() { meter.RegisterCallback(cb) }\n", true},
		"producer option":           {"internal/app/app.go", "package p\nvar _ = sdkmetric.WithProducer(p)\n", true},
		"producer method value":     {"internal/app/app.go", "package p\nvar f = sdkmetric.WithProducer\n", true},
		"producer config key":       {"internal/app/options.go", "package p\nvar _ = Options{MetricProducers: ps}\n", true},
		"producer assignment":       {"internal/app/options.go", "package p\nfunc f() { o.MetricProducers = ps }\n", true},
		"producer set in reader":    {collectGuardScheduled, "package p\nfunc newScheduledMetricReader() { o.MetricProducers = ps }\n", true},
		"global meter provider":     {"internal/app/app.go", "package p\nfunc f() { otel.SetMeterProvider(mp) }\n", true},
		"library meter provider":    {"internal/app/app.go", "package p\nvar _ = runtime.WithMeterProvider(mp)\n", true},
	} {
		t.Run(name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, tc.rel, tc.source, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			violations, _ := collectGuardScan(fset, tc.rel, file)
			if (len(violations) != 0) != tc.wantBad {
				t.Fatalf("violations = %v, want bad=%v", violations, tc.wantBad)
			}
		})
	}
}
