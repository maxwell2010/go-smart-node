// replay-restore restores an existing format-2 snapshot only into a new local home.
// It never connects to peers, signs transactions, or reads a running node database.
package main

import (
	"bitbucket.org/decimalteam/go-smart-node/app"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/cosmos/cosmos-sdk/snapshots"
	snapshottypes "github.com/cosmos/cosmos-sdk/snapshots/types"
	"github.com/cosmos/ibc-go/v6/testing/simapp"
	"github.com/decimalteam/ethermint/encoding"
	"github.com/tendermint/tendermint/libs/log"
	dbm "github.com/tendermint/tm-db"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	input := flag.String("input", "", "immutable snapshot input root")
	home := flag.String("home", "", "new, nonexistent disposable application home")
	expected := flag.String("trusted-app-hash", "", "AppHash from block 34271001")
	flag.Parse()
	trusted, err := hex.DecodeString(*expected)
	if err != nil || len(trusted) != 32 {
		return fmt.Errorf("a trusted 32-byte AppHash is required")
	}
	if *input == "" || *home == "" {
		return fmt.Errorf("input and new home required")
	}
	in, err := filepath.Abs(*input)
	if err != nil {
		return err
	}
	out, err := filepath.Abs(*home)
	if err != nil {
		return err
	}
	rel, _ := filepath.Rel(in, out)
	if rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)) {
		return fmt.Errorf("home must be outside immutable input")
	}
	if err = os.Mkdir(out, 0700); err != nil {
		return fmt.Errorf("refuse existing or inaccessible home: %w", err)
	}
	if err = os.MkdirAll(filepath.Join(out, "config"), 0700); err != nil {
		return err
	}
	genesis, err := os.ReadFile(filepath.Join(in, "config", "genesis.json"))
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(out, "config", "genesis.json"), genesis, 0600); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(out, "data"), 0700); err != nil {
		return err
	}
	meta := snapshottypes.Snapshot{Height: 34271000, Format: 2, Chunks: 13}
	total := sha256.New()
	for i := uint32(0); i < meta.Chunks; i++ {
		f, e := os.Open(filepath.Join(in, "snapshots", "34271000", "2", fmt.Sprint(i)))
		if e != nil {
			return e
		}
		h := sha256.New()
		_, e = io.Copy(io.MultiWriter(h, total), f)
		f.Close()
		if e != nil {
			return e
		}
		meta.Metadata.ChunkHashes = append(meta.Metadata.ChunkHashes, h.Sum(nil))
	}
	meta.Hash = total.Sum(nil)
	database, err := dbm.NewGoLevelDB("application", filepath.Join(out, "data"))
	if err != nil {
		return err
	}
	defer database.Close()
	snapDB := dbm.NewMemDB()
	defer snapDB.Close()
	store, err := snapshots.NewStore(snapDB, filepath.Join(out, "data", "snapshots"))
	if err != nil {
		return err
	}
	dsc := app.NewDSC(log.NewNopLogger(), database, nil, true, map[int64]bool{}, out, 0, encoding.MakeConfig(app.ModuleBasics), simapp.EmptyAppOptions{})
	manager := snapshots.NewManager(store, snapshottypes.SnapshotOptions{}, dsc.CommitMultiStore(), nil, log.NewNopLogger())
	if err = manager.Restore(meta); err != nil {
		return err
	}
	for i := uint32(0); i < meta.Chunks; i++ {
		data, e := os.ReadFile(filepath.Join(in, "snapshots", "34271000", "2", fmt.Sprint(i)))
		if e != nil {
			return e
		}
		done, e := manager.RestoreChunk(data)
		if e != nil {
			return fmt.Errorf("chunk %d: %w", i, e)
		}
		fmt.Fprintf(os.Stderr, "restored chunk %d/%d\n", i+1, meta.Chunks)
		if done != (i == meta.Chunks-1) {
			return fmt.Errorf("unexpected completion at chunk %d", i)
		}
	}
	id := dsc.CommitMultiStore().LastCommitID()
	result := map[string]interface{}{"height": id.Version, "app_hash": hex.EncodeToString(id.Hash), "trusted_app_hash": *expected, "matches_trusted": strings.EqualFold(hex.EncodeToString(id.Hash), *expected), "snapshot_sha256": hex.EncodeToString(meta.Hash)}
	b, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(b))
	if err = os.WriteFile(filepath.Join(out, "restore-report.json"), b, 0600); err != nil {
		return err
	}
	if id.Version != int64(meta.Height) || !strings.EqualFold(hex.EncodeToString(id.Hash), *expected) {
		return fmt.Errorf("restored AppHash does not match trusted block header")
	}
	return nil
}
