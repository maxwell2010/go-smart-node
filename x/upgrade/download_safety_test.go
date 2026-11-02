package upgrade

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/upgrade/types"
	"github.com/stretchr/testify/require"
	"github.com/tendermint/tendermint/libs/log"
)

func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func TestPlanMappingRejectsInvalidChecksums(t *testing.T) {
	for _, info := range []string{`null`, `[]`, `{"linux":[]}`, `{"linux":[""]}`, `{"linux":["abcd"]}`, `{"linux":[123]}`} {
		require.Nil(t, planMapping(types.Plan{Info: info}))
	}
	require.NotNil(t, planMapping(types.Plan{Info: `{"linux":["` + digest("ok") + `"]}`}))
	require.Error(t, changeBinary(types.Plan{Info: `{"` + osArchForURL() + `":[]}`}))
}

func TestDownloadFailurePreservesExistingFile(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "candidate")
	require.NoError(t, os.WriteFile(dst, []byte("existing"), 0600))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("wrong")) }))
	defer server.Close()
	ctx := sdk.Context{}.WithLogger(log.NewNopLogger())
	require.Error(t, DownloadSecured(ctx, server.URL, dst, digest("expected")))
	b, err := os.ReadFile(dst)
	require.NoError(t, err)
	require.Equal(t, "existing", string(b))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestBoundedHTTPRequests(t *testing.T) {
	oldDownload, oldProbe := downloadClient, probeClient
	t.Cleanup(func() { downloadClient, probeClient = oldDownload, oldProbe })
	downloadClient = &http.Client{Timeout: 30 * time.Millisecond}
	probeClient = &http.Client{Timeout: 30 * time.Millisecond}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	require.False(t, doesPageExist(server.URL))
	err := downloadFile(server.URL, filepath.Join(t.TempDir(), "file"))
	require.Error(t, err)
}

func TestDownloadRetriesAfterFailure(t *testing.T) {
	name := t.Name()
	t.Cleanup(func() { downloadMu.Lock(); delete(downloadAttempts, name); downloadMu.Unlock() })
	release := make(chan struct{})
	require.True(t, queueDownloadAt(name, time.Now(), func() error { <-release; return errors.New("injected failure") }))
	require.False(t, queueDownloadAt(name, time.Now(), func() error { t.Error("overlap"); return nil }))
	close(release)
	require.Eventually(t, func() bool { downloadMu.Lock(); defer downloadMu.Unlock(); return !downloadAttempts[name].running }, time.Second, time.Millisecond)
	require.False(t, queueDownloadAt(name, time.Now(), func() error { return nil }))
	require.True(t, queueDownloadAt(name, time.Now().Add(2*time.Minute), func() error { return nil }))
	require.Eventually(t, func() bool { downloadMu.Lock(); defer downloadMu.Unlock(); return downloadAttempts[name].complete }, time.Second, time.Millisecond)
	require.False(t, queueDownloadAt(name, time.Now().Add(3*time.Minute), func() error { return nil }))
}

func TestVerifiedReplacementNeverUnlinksCurrentFirst(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "dscd")
	candidate := filepath.Join(dir, "candidate")
	require.NoError(t, os.WriteFile(current, []byte("old"), 0700))
	require.NoError(t, os.WriteFile(candidate, []byte("new"), 0600))
	require.Error(t, replaceBinary(current, candidate, digest("new"), func(string) bool { return false }))
	b, err := os.ReadFile(current)
	require.NoError(t, err)
	require.Equal(t, "old", string(b))
	err = replaceBinary(current, candidate, digest("new"), func(string) bool { return true })
	if err != nil {
		require.Equal(t, "windows", runtime.GOOS)
		b, e := os.ReadFile(current)
		require.NoError(t, e)
		require.Equal(t, "old", string(b))
		return
	}
	b, err = os.ReadFile(current)
	require.NoError(t, err)
	require.Equal(t, "new", string(b))
}

func TestValidationCannotMutateCandidate(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "dscd")
	candidate := filepath.Join(dir, "candidate")
	require.NoError(t, os.WriteFile(current, []byte("old"), 0700))
	require.NoError(t, os.WriteFile(candidate, []byte("new"), 0600))
	err := replaceBinary(current, candidate, digest("new"), func(path string) bool { return os.WriteFile(path, []byte("changed"), 0700) == nil })
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "changed"))
	b, e := os.ReadFile(current)
	require.NoError(t, e)
	require.Equal(t, "old", string(b))
}

func TestDownloadPublishesOnlyVerifiedCandidate(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "candidate")
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1); w.Write([]byte("verified binary")) }))
	defer server.Close()
	ctx := sdk.Context{}.WithLogger(log.NewNopLogger())
	require.NoError(t, DownloadSecured(ctx, server.URL, destination, digest("verified binary")))
	require.True(t, checkFile(destination, digest("verified binary")))
	require.NoError(t, DownloadSecured(ctx, server.URL, destination, digest("verified binary")))
	require.Equal(t, int32(1), count.Load(), "already verified candidate must not be downloaded again")
}

func TestRenameFailureDoesNotRemoveDestination(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "dscd")
	candidate := filepath.Join(dir, "candidate")
	require.NoError(t, os.Mkdir(current, 0700))
	sentinel := filepath.Join(current, "existing")
	require.NoError(t, os.WriteFile(sentinel, []byte("preserved"), 0600))
	require.NoError(t, os.WriteFile(candidate, []byte("new"), 0600))
	require.Error(t, replaceBinary(current, candidate, digest("new"), func(string) bool { return true }))
	b, err := os.ReadFile(sentinel)
	require.NoError(t, err)
	require.Equal(t, "preserved", string(b))
	require.True(t, checkFile(candidate, digest("new")))
}
