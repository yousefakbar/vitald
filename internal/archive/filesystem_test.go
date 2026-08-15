package archive

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFilesystemSave(t *testing.T) {
	root := t.TempDir()
	archive := Filesystem{Root: root}
	file, err := archive.Save(context.Background(), "steps", "run", 1, []byte(`{"ok":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if file.Size != 11 || len(file.Checksum) != 64 {
		t.Fatalf("unexpected metadata: %+v", file)
	}
	data, err := os.ReadFile(filepath.Join(root, "googlehealth", "steps", "run", "page-0001.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"ok":true}` {
		t.Fatalf("unexpected data: %s", data)
	}
	if _, err := archive.Save(context.Background(), "steps", "run", 1, data); err == nil {
		t.Fatal("expected overwrite to be rejected")
	}
}
