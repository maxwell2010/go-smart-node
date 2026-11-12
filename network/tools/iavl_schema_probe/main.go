// iavl_schema_probe inspects the bank store's IAVL format in an offline node.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	gogotypes "github.com/cosmos/gogoproto/types"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"
)

const bankPrefix = "s/k:bank/"

type schemaReport struct {
	StorageVersion    string `json:"storageVersion"`
	FastNodes         int    `json:"fastNodes"`
	CommittedHeight   int64  `json:"committedHeight"`
	FastStorageHeight *int64 `json:"fastStorageHeight,omitempty"`
}

func inspect(home string) (schemaReport, error) {
	var result schemaReport
	database, err := leveldb.OpenFile(filepath.Join(home, "data", "application.db"), &opt.Options{
		ReadOnly:       true,
		ErrorIfMissing: true,
	})
	if err != nil {
		return result, fmt.Errorf("open offline application.db: %w", err)
	}
	defer database.Close()

	version, err := database.Get([]byte(bankPrefix+"mstorage_version"), nil)
	switch {
	case errors.Is(err, leveldb.ErrNotFound), err == nil && string(version) == "1.0.0":
		result.StorageVersion = "1.0.0"
	case err != nil:
		return result, fmt.Errorf("read bank IAVL storage version: %w", err)
	default:
		encodedHeight, found := strings.CutPrefix(string(version), "1.1.0-")
		height, parseErr := strconv.ParseInt(encodedHeight, 10, 64)
		if !found || parseErr != nil || height < 0 || strconv.FormatInt(height, 10) != encodedHeight {
			return result, errors.New("unexpected bank IAVL storage version")
		}
		result.StorageVersion = "1.1.0"
		result.FastStorageHeight = &height
	}

	latest, err := database.Get([]byte("s/latest"), nil)
	if err != nil {
		return result, fmt.Errorf("read latest application version: %w", err)
	}
	if err := gogotypes.StdInt64Unmarshal(&result.CommittedHeight, latest); err != nil || result.CommittedHeight <= 0 {
		return result, errors.New("invalid latest committed application height")
	}

	iterator := database.NewIterator(util.BytesPrefix([]byte(bankPrefix+"f")), nil)
	defer iterator.Release()
	for iterator.Next() {
		result.FastNodes++
	}
	if err := iterator.Error(); err != nil {
		return result, fmt.Errorf("read bank IAVL fast-node index: %w", err)
	}
	return result, nil
}

func main() {
	home := flag.String("home", "", "home directory of a stopped test node or its offline backup")
	flag.Parse()
	if *home == "" || flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	report, err := inspect(*home)
	if err == nil {
		err = json.NewEncoder(os.Stdout).Encode(report)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "iavl-schema-probe:", err)
		os.Exit(1)
	}
}
