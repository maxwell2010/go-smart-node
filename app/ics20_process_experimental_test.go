//go:build experimental_ibc

package app

import (
	cmdcfg "bitbucket.org/decimalteam/go-smart-node/cmd/config"
	"context"
	"encoding/json"
	"fmt"
	sdkconfig "github.com/cosmos/cosmos-sdk/server/config"
	etherconfig "github.com/decimalteam/ethermint/server/config"
	"github.com/stretchr/testify/require"
	tmconfig "github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/p2p"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in process test owns its entire fresh directory and both child PIDs.
func TestExperimentalICS20ProcessSmoke(t *testing.T) {
	if os.Getenv("DSC_IBC_PROCESS_SMOKE") != "1" {
		t.Skip("set DSC_IBC_PROCESS_SMOKE=1 to build and run two disposable local processes")
	}
	root := t.TempDir()
	dest := filepath.Join(root, "networks")
	exportIsolatedICS20Fixtures(t, dest)
	binary := filepath.Join(root, "dscd-test.exe")
	build := exec.Command("go", "build", "-mod=readonly", "-tags", "experimental_ibc", "-o", binary, "../cmd/dscd")
	hideTestWindow(build)
	output, err := build.CombinedOutput()
	require.NoError(t, err, "build: %s", string(output))
	for i, id := range []string{"decimal_900001-1", "decimal_900002-1"} {
		home := filepath.Join(dest, id)
		for _, port := range []int{26757 + i*100, 26756 + i*100} {
			listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			require.NoError(t, err, "refusing to use an occupied test port")
			require.NoError(t, listener.Close())
		}
		template, raw := etherconfig.AppConfig(cmdcfg.BaseDenom)
		config := raw.(etherconfig.Config)
		config.API.Enable = false
		config.GRPC.Enable = false
		config.GRPCWeb.Enable = false
		config.JSONRPC.Enable = false
		config.MinGasPrices = "0del"
		sdkconfig.SetConfigTemplate(template)
		sdkconfig.WriteConfigFile(filepath.Join(home, "config", "app.toml"), config)
		_, err = p2p.LoadOrGenNodeKey(filepath.Join(home, "config", "node_key.json"))
		require.NoError(t, err)
		cfg := tmconfig.DefaultConfig().SetRoot(home)
		cfg.Moniker = id
		cfg.RPC.ListenAddress = fmt.Sprintf("tcp://127.0.0.1:%d", 26757+i*100)
		cfg.P2P.ListenAddress = fmt.Sprintf("tcp://127.0.0.1:%d", 26756+i*100)
		cfg.P2P.PexReactor = false
		cfg.P2P.Seeds = ""
		cfg.P2P.PersistentPeers = ""
		cfg.Consensus.TimeoutCommit = time.Second
		tmconfig.WriteConfigFile(filepath.Join(home, "config", "config.toml"), cfg)
		log, err := os.OpenFile(filepath.Join(root, id+".log"), os.O_CREATE|os.O_WRONLY, 0600)
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		cmd := exec.CommandContext(ctx, binary, "start", "--home", home)
		hideTestWindow(cmd)
		cmd.Stdout = log
		cmd.Stderr = log
		require.NoError(t, cmd.Start())
		t.Cleanup(func() { cancel(); _ = cmd.Wait(); _ = log.Close() })
	}
	client := http.Client{Timeout: time.Second}
	deadline := time.Now().Add(60 * time.Second)
	heights := map[int]int64{}
	for time.Now().Before(deadline) {
		for _, port := range []int{26757, 26857} {
			resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/status", port))
			if err != nil {
				continue
			}
			var result struct {
				Result struct {
					NodeInfo struct {
						Network string `json:"network"`
					} `json:"node_info"`
					Sync struct {
						Height int64 `json:"latest_block_height,string"`
					} `json:"sync_info"`
				} `json:"result"`
			}
			err = json.NewDecoder(resp.Body).Decode(&result)
			_ = resp.Body.Close()
			if err == nil && result.Result.NodeInfo.Network == map[int]string{26757: "decimal_900001-1", 26857: "decimal_900002-1"}[port] {
				heights[port] = result.Result.Sync.Height
			}
		}
		if heights[26757] >= 3 && heights[26857] >= 3 {
			t.Logf("Two isolated OS processes advanced: %v", heights)
			return
		}
		time.Sleep(time.Second)
	}
	for _, id := range []string{"decimal_900001-1", "decimal_900002-1"} {
		data, _ := os.ReadFile(filepath.Join(root, id+".log"))
		if len(data) > 5000 {
			data = data[len(data)-5000:]
		}
		t.Logf("%s startup tail: %s", id, data)
	}
	t.Fatalf("local chains did not advance: %v", heights)
}
