package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicCheckpointReplacesShorterReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	for _, body := range []string{`[{"height":123456789}]`, `[]`} {
		if err := atomicCheckpoint(path, []byte(body)); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != body {
			t.Fatalf("checkpoint %s error %v", data, err)
		}
	}
}
