package config

import (
	"reflect"
	"strings"
)

// ReloadClass describes when a configuration value takes effect after startup.
// The value is intentionally closed: a path can have its contents re-read live,
// but changing the configured path itself still requires a restart.
type ReloadClass string

const (
	// ReloadRestart means changing the configuration value requires a process
	// restart.
	ReloadRestart ReloadClass = "restart"
	// ReloadFileContent means the value is a fixed filesystem path whose contents
	// are re-read while the process runs. Changing the path still requires a
	// restart.
	ReloadFileContent ReloadClass = "file_content"
)

// ReloadRow is one leaf configuration key and its startup/reload behavior.
type ReloadRow struct {
	Key   string
	Class ReloadClass
}

// ReloadClassifications returns one row for every leaf key in Config. It uses
// the yaml struct tags for the same dotted lower_snake_case key convention as
// envReferenceRows and pathFields. Struct fields are recursed into; maps,
// slices, and scalar-like named types are leaves, matching envReferenceRows'
// treatment of structured file-only values and scalar lists.
func ReloadClassifications() []ReloadRow {
	return reloadRows(reflect.TypeFor[Config](), "")
}

func reloadRows(t reflect.Type, prefix string) []ReloadRow {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	var rows []ReloadRow
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported Config bookkeeping fields
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		key := name
		if prefix != "" {
			key = prefix + keyDelim + name
		}

		ft := f.Type
		if ft.Kind() == reflect.Struct ||
			(ft.Kind() == reflect.Pointer && ft.Elem().Kind() == reflect.Struct) {
			rows = append(rows, reloadRows(ft, key)...)
			continue
		}
		rows = append(rows, ReloadRow{Key: key, Class: ReloadClass(f.Tag.Get("reload"))})
	}
	return rows
}
