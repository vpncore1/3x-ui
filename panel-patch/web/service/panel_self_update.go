package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v2/config"
	"github.com/mhsanaei/3x-ui/v2/logger"
)

const (
	panelUpdateRepoOwner = "vpncore1"
	panelUpdateRepoName  = "3x-ui"
	panelUpdateBranch    = "main"
	panelUpdateStampPath = "/usr/local/x-ui/panel-patch.version"
	panelUpdateLogPath   = "/tmp/panel-self-update.log"
	panelUpdateLockPath  = "/tmp/panel-self-update.lock"
	panelUpdateScriptURL = "https://raw.githubusercontent.com/vpncore1/3x-ui/main/upgrade.sh"
)

var panelUpdateMu sync.Mutex

// PanelUpdateInfo is returned to the UI for the manual update button.
type PanelUpdateInfo struct {
	CurrentVersion string `json:"currentVersion"`
	LocalSHA       string `json:"localSha"`
	RemoteSHA      string `json:"remoteSha"`
	RemoteMessage  string `json:"remoteMessage"`
	UpdateAvailable bool  `json:"updateAvailable"`
	Repo           string `json:"repo"`
	Branch         string `json:"branch"`
	Updating       bool   `json:"updating"`
	LastLogTail    string `json:"lastLogTail"`
}

// GetPanelUpdateInfo compares the installed stamp with the latest commit on our repo.
// Never applies updates — check only.
func (s *ServerService) GetPanelUpdateInfo() (*PanelUpdateInfo, error) {
	info := &PanelUpdateInfo{
		CurrentVersion: strings.TrimSpace(config.GetVersion()),
		LocalSHA:       readPanelStampSHA(),
		Repo:           panelUpdateRepoOwner + "/" + panelUpdateRepoName,
		Branch:         panelUpdateBranch,
		Updating:       panelUpdateInProgress(),
		LastLogTail:    tailFile(panelUpdateLogPath, 4000),
	}

	remoteSHA, remoteMsg, err := fetchGithubLatestCommit(panelUpdateRepoOwner, panelUpdateRepoName, panelUpdateBranch)
	if err != nil {
		return info, err
	}
	info.RemoteSHA = remoteSHA
	info.RemoteMessage = remoteMsg
	if info.LocalSHA != "" && remoteSHA != "" && !strings.EqualFold(info.LocalSHA, remoteSHA) {
		info.UpdateAvailable = true
	}
	// Fresh install without stamp: treat as update available when remote exists
	// so the admin can sync once, but still only on manual click.
	if info.LocalSHA == "" && remoteSHA != "" {
		info.UpdateAvailable = true
	}
	return info, nil
}

// StartPanelSelfUpdate launches upgrade.sh from our public repo in the background.
// Manual-only: nothing calls this except the admin UI button.
func (s *ServerService) StartPanelSelfUpdate() error {
	panelUpdateMu.Lock()
	defer panelUpdateMu.Unlock()

	if panelUpdateInProgress() {
		return fmt.Errorf("an update is already running; see %s", panelUpdateLogPath)
	}

	// Create lock early so concurrent clicks are rejected.
	if err := os.WriteFile(panelUpdateLockPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0o644); err != nil {
		return err
	}

	script := fmt.Sprintf(`
set -euo pipefail
exec >%q 2>&1
echo "==> panel self-update started at $(date -Is)"
echo "==> source: %s"
sleep 2
export DEBIAN_FRONTEND=noninteractive
curl -fsSL %q -o /tmp/panel-upgrade.sh
chmod +x /tmp/panel-upgrade.sh
bash /tmp/panel-upgrade.sh
echo "==> panel self-update finished at $(date -Is)"
rm -f %q
`, panelUpdateLogPath, panelUpdateScriptURL, panelUpdateScriptURL, panelUpdateLockPath)

	cmd := exec.Command("bash", "-c", script)
	// Detach from panel process so upgrade can stop/restart x-ui.
	cmd.SysProcAttr = sysProcAttrDetached()
	if err := cmd.Start(); err != nil {
		_ = os.Remove(panelUpdateLockPath)
		return err
	}
	logger.Infof("panel self-update started pid=%d log=%s", cmd.Process.Pid, panelUpdateLogPath)
	// Reap in background; process is detached via Setsid so it survives panel restart.
	go func() { _ = cmd.Wait() }()
	return nil
}

func readPanelStampSHA() string {
	b, err := os.ReadFile(panelUpdateStampPath)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(b), "\n")
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		// Accept either bare SHA or "sha=<...>"
		if strings.HasPrefix(ln, "sha=") {
			return strings.TrimSpace(strings.TrimPrefix(ln, "sha="))
		}
		if len(ln) >= 7 && isHex(ln) {
			return ln
		}
	}
	return ""
}

func isHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

func panelUpdateInProgress() bool {
	st, err := os.Stat(panelUpdateLockPath)
	if err != nil {
		return false
	}
	// Stale lock older than 45 minutes → ignore
	if time.Since(st.ModTime()) > 45*time.Minute {
		_ = os.Remove(panelUpdateLockPath)
		return false
	}
	return true
}

func fetchGithubLatestCommit(owner, repo, branch string) (sha, message string, err error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/commits/%s", owner, repo, branch)
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "3x-ui-sub-balancer-panel")
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("github api status %d", resp.StatusCode)
	}
	var parsed struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", "", err
	}
	msg := strings.TrimSpace(parsed.Commit.Message)
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = strings.TrimSpace(msg[:i])
	}
	return parsed.SHA, msg, nil
}

func tailFile(path string, max int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(b) <= max {
		return string(b)
	}
	return string(b[len(b)-max:])
}

// WritePanelUpdateStamp writes the installed patch commit SHA (called from build scripts / tests).
func WritePanelUpdateStamp(sha, version string) error {
	sha = strings.TrimSpace(sha)
	if sha == "" {
		return fmt.Errorf("empty sha")
	}
	dir := filepath.Dir(panelUpdateStampPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf("sha=%s\nversion=%s\nupdated_at=%s\nrepo=%s/%s\nbranch=%s\n",
		sha, strings.TrimSpace(version), time.Now().UTC().Format(time.RFC3339),
		panelUpdateRepoOwner, panelUpdateRepoName, panelUpdateBranch)
	return os.WriteFile(panelUpdateStampPath, []byte(content), 0o644)
}
