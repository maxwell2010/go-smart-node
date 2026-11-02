package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDisposableReferenceProcess(t *testing.T) {
	if os.Getenv("MC_IBC_REFERENCE_PROCESS_SMOKE") != "1" {
		t.Skip("opt in to build and run only a disposable loopback reference process")
	}
	base := t.TempDir()
	binary := filepath.Join(base, "ibcreference")
	build := exec.Command("go", "build", "-p", "1", "-mod=readonly", "-o", binary, ".")
	out, err := build.CombinedOutput()
	require.NoError(t, err, "build: %s", out)
	ports := map[int]bool{}
	freePort := func() int {
		for {
			listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, listenErr)
			port := listener.Addr().(*net.TCPAddr).Port
			require.NoError(t, listener.Close())
			if !ports[port] {
				ports[port] = true
				return port
			}
		}
	}
	rpc, p2p, grpc := freePort(), freePort(), freePort()
	home := filepath.Join(base, "chain")
	prepare := exec.Command(binary, "prepare", "--home", home, "--rpc-port", strconv.Itoa(rpc), "--p2p-port", strconv.Itoa(p2p), "--grpc-port", strconv.Itoa(grpc))
	out, err = prepare.CombinedOutput()
	require.NoError(t, err, "prepare: %s", out)
	// Preparing an existing home must never overwrite its generated keys/state.
	again := exec.Command(binary, "prepare", "--home", home, "--rpc-port", strconv.Itoa(rpc), "--p2p-port", strconv.Itoa(p2p), "--grpc-port", strconv.Itoa(grpc))
	out, err = again.CombinedOutput()
	require.Error(t, err, "repeated prepare must refuse existing output")
	require.True(t, strings.Contains(string(out), "refuse existing output"))
	logFile, err := os.OpenFile(filepath.Join(base, "reference.log"), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	require.NoError(t, err)
	t.Cleanup(func() { _ = logFile.Close() })
	process := exec.Command(binary, "start", "--home", home, "--log_level", "error")
	process.Stdout = logFile
	process.Stderr = logFile
	require.NoError(t, process.Start())
	done := make(chan error, 1)
	go func() { done <- process.Wait(); close(done) }()
	t.Cleanup(func() {
		_ = process.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(8 * time.Second):
			_ = process.Process.Kill()
			<-done
		}
	})
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case exitErr := <-done:
			logs, _ := os.ReadFile(filepath.Join(base, "reference.log"))
			t.Fatalf("own process exited: %v; %s", exitErr, logs)
		default:
		}
		response, getErr := client.Get("http://127.0.0.1:" + strconv.Itoa(rpc) + "/status")
		if getErr == nil {
			var status struct {
				Result struct {
					NodeInfo struct {
						Network string `json:"network"`
					} `json:"node_info"`
					SyncInfo struct {
						Height string `json:"latest_block_height"`
					} `json:"sync_info"`
				} `json:"result"`
			}
			decodeErr := json.NewDecoder(response.Body).Decode(&status)
			_ = response.Body.Close()
			if decodeErr == nil {
				require.Equal(t, chainID, status.Result.NodeInfo.Network)
				height, _ := strconv.ParseInt(status.Result.SyncInfo.Height, 10, 64)
				if height >= 3 {
					t.Logf("independent SDK reference advanced to height %d", height)
					return
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	logs, _ := os.ReadFile(filepath.Join(base, "reference.log"))
	t.Fatalf("reference height deadline: %s", logs)
}
