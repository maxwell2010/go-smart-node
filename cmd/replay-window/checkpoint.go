package main

import (
	"os"
	"path/filepath"
)

// atomicCheckpoint publishes a verified local checkpoint only after its bytes are synced.
func atomicCheckpoint(path string, payload []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".replay-checkpoint-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(payload); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
