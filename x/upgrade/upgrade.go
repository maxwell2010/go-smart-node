package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"time"

	"gopkg.in/ini.v1"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/cosmos/cosmos-sdk/x/upgrade/types"

	cmdcfg "bitbucket.org/decimalteam/go-smart-node/cmd/config"
)

// DownloadSecured downloads file from provided URL to the specified path and checks it's hash.
func DownloadSecured(ctx sdk.Context, sourceURL string, destination string, hash string) error {
	if !validSHA256(hash) {
		return fmt.Errorf("invalid SHA-256 checksum")
	}
	if checkFile(destination, hash) {
		return nil
	}
	ctx.Logger().Info("downloading verified upgrade binary", "url", sourceURL)
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".upgrade-download-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if err = tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	defer os.Remove(name)
	if err = downloadFile(sourceURL, name); err != nil {
		return err
	}
	if !checkFile(name, hash) {
		return fmt.Errorf("download checksum mismatch")
	}
	// Rename replaces the destination atomically where supported; failure leaves it intact.
	return os.Rename(name, destination)
}

const downloadTimeout = 2 * time.Minute
const probeTimeout = 5 * time.Second

var downloadClient = &http.Client{Timeout: downloadTimeout}
var probeClient = &http.Client{Timeout: probeTimeout}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == hex.EncodeToString(decoded)
}

// downloadFile uses a bounded request and syncs the completed temporary file.
func downloadFile(sourceURL string, destination string) error {
	resp, err := downloadClient.Get(sourceURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download reply code %d", resp.StatusCode)
	}
	out, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err = io.Copy(out, resp.Body); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	return out.Close()
}

// checkFile gets the hash of the download file, then checks what was in the transaction.
func checkFile(path, hash string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hash == hex.EncodeToString(h.Sum(nil))
}

// getDownloadFileName generates name of a download file.
func getDownloadFileName(name string) string {
	ex, _ := os.Executable()
	return filepath.Join(filepath.Dir(ex), fmt.Sprintf("%s.nv", name))
}

// resolveDownloadURL returns exact URL to download correct binary.
func resolveDownloadURL(s string) string {
	// example: "linux/ubuntu/20.04"
	u, err := url.Parse(osArchForURL())
	if err != nil {
		return ""
	}

	// example: "http://127.0.0.1/90500/decd"
	myUrl, err := url.Parse(s)
	if err != nil {
		return ""
	}

	// result: "http://127.0.0.1/90500/linux/ubuntu/20.04/decd"
	return fmt.Sprintf("%s/%s", myUrl.ResolveReference(u), path.Base(myUrl.Path))
}

// doesPageExist checks if the page at provided URL exists.
func doesPageExist(s string) bool {
	resp, err := probeClient.Head(s)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == 200
}

// changeBinary changes the old binary to a new one.
func changeBinary(plan types.Plan) error {
	mapping := planMapping(plan)
	hashes := mapping[osArchForURL()]
	if len(hashes) == 0 {
		return fmt.Errorf("missing or invalid platform checksum mapping")
	}
	ex, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable: %w", err)
	}
	current := filepath.Join(filepath.Dir(ex), cmdcfg.AppBinName)
	return replaceBinary(current, getDownloadFileName(cmdcfg.AppBinName), hashes[0], isRunSuccess)
}

// replaceBinary never unlinks the running executable. If the platform cannot replace
// it with rename, leave it in place and require operator/supervisor intervention.
func replaceBinary(current, candidate, hash string, validate func(string) bool) error {
	if !validSHA256(hash) || !checkFile(candidate, hash) {
		return fmt.Errorf("candidate checksum mismatch")
	}
	mode, err := getMode(current)
	if err != nil {
		return err
	}
	if err := markExecutableWithMode(candidate, mode); err != nil {
		return err
	}
	if !validate(candidate) {
		return fmt.Errorf("candidate version check failed")
	}
	if !checkFile(candidate, hash) {
		return fmt.Errorf("candidate changed during validation")
	}
	if err := os.Rename(candidate, current); err != nil {
		return fmt.Errorf("replace executable without unlink: %w", err)
	}
	return nil
}

// getMode returns file mode.
func getMode(path string) (os.FileMode, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stating binary: %w", err)
	}
	return info.Mode().Perm(), nil
}

// markExecutableWithMode sets executable flag for specified file.
func markExecutableWithMode(path string, mode os.FileMode) error {
	return os.Chmod(path, mode|0111)
}

// isRunSuccess runs specified file with argument `version` to check if it runs.
func isRunSuccess(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version")
	err := cmd.Run()
	return err == nil
}

// osArchForURL detects and returns OS to create an URL.
func osArchForURL() string {
	switch runtime.GOOS {
	case "windows", "darwin":
		return runtime.GOOS
	case "linux":
		distr := readOSRelease("ID")
		if distr == "" {
			distr = "<unknown>"
		}
		version := readOSRelease("VERSION_ID")
		if version == "" {
			version = "<unknown>"
		}
		return fmt.Sprintf("linux/%s/%s", distr, version)
	default:
		return runtime.GOOS
	}
}

// readOSRelease reads the file under /etc/os-release to get the distribution name or version.
func readOSRelease(key string) string {
	const cfgfile = "/etc/os-release"
	cfg, err := ini.Load(cfgfile)
	if err != nil {
		return ""
	}
	return cfg.Section("").Key(key).String()
}

// planMapping returns plans info as map.
func planMapping(plan types.Plan) map[string][]string {
	var mapping map[string][]string
	err := json.Unmarshal([]byte(plan.Info), &mapping)
	if err != nil {
		return nil
	}
	for platform, hashes := range mapping {
		if platform == "" || len(hashes) == 0 {
			return nil
		}
		for _, hash := range hashes {
			if !validSHA256(hash) {
				return nil
			}
		}
	}
	return mapping
}
