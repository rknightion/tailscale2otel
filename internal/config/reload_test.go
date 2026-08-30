package config

import "testing"

// TestReloadClassificationCoversEveryKey is the drift gate for the reload
// classification tags. A new leaf without a tag (or with a misspelled value)
// must fail while naming the affected config key.
func TestReloadClassificationCoversEveryKey(t *testing.T) {
	rows := ReloadClassifications()
	if len(rows) == 0 {
		t.Fatal("ReloadClassifications returned no config keys")
	}

	for _, row := range rows {
		switch row.Class {
		case ReloadRestart, ReloadFileContent:
			// valid
		default:
			t.Errorf("config key %q has missing or invalid reload classification %q", row.Key, row.Class)
		}
	}
}

// TestFileContentReloadKeysArePathFields makes the file_content claims
// evidence-based: a key may only claim live file-content reload when the
// loader already treats it as a filesystem path.
func TestFileContentReloadKeysArePathFields(t *testing.T) {
	pathKeys := make(map[string]bool)
	for _, field := range Default().pathFields() {
		pathKeys[field.key] = true
	}

	for _, row := range ReloadClassifications() {
		if row.Class == ReloadFileContent && !pathKeys[row.Key] {
			t.Errorf("config key %q is classified file_content but is not a path field", row.Key)
		}
	}
}
