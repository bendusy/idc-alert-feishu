package scripts_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runBash(t *testing.T, script string) string {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "TASK_PROGRESS_SLEEP_SECONDS=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash failed: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestPureFunctions(t *testing.T) {
	out := runBash(t, `
set -euo pipefail
TASK_PROGRESS_SOURCE_ONLY=1 . ./task-progress
human_bytes 1099511627776
human_duration 5400
printf '\n'
calc_rate_bps 1000 500 10 20
calc_eta_seconds 1000 50
emit_policy 3600 0
emit_policy 10800 0
emit_policy 30000 3
`)
	want := strings.Join([]string{
		"1.0TiB",
		"1h30m",
		"50",
		"20",
		"send 0 1",
		"skip 1 2",
		"send 0 4",
	}, "\n")
	if out != want {
		t.Fatalf("unexpected pure function output:\nwant:\n%s\n\ngot:\n%s", want, out)
	}
}

func TestSendRetriesAfterRateLimit(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/progress/idc-infra" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if hits == 1 {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	cmd := exec.Command("bash", "./task-progress", "send",
		"--endpoint", server.URL,
		"--group", "idc-infra",
		"--summary", "x",
		"--detail", "y",
	)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "TASK_PROGRESS_SLEEP_SECONDS=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("send failed: %v\n%s", err, out)
	}
	if hits != 2 {
		t.Fatalf("expected 2 attempts, got %d", hits)
	}
}

func TestWatchWorksUnderCronLikeEnvironment(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		body = string(data)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	dir := t.TempDir()
	probe := filepath.Join(dir, "fake.conf")
	stateDir := filepath.Join(dir, "state")
	cronDir := filepath.Join(dir, "cron")
	if err := os.WriteFile(probe, []byte(`
GROUP="idc-infra"
TITLE="fake task"
remaining_bytes() { printf '100\n'; }
is_done() { return 1; }
is_healthy() { return 0; }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "./task-progress", "watch",
		"--probe", probe,
		"--endpoint", server.URL,
		"--state-dir", stateDir,
		"--cron-dir", cronDir,
	)
	cmd.Dir = "."
	cmd.Env = []string{
		"PATH=/usr/bin:/bin",
		"TASK_PROGRESS_NOW=1000",
		"TASK_PROGRESS_SLEEP_SECONDS=0",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("watch failed: %v\n%s", err, out)
	}
	if !strings.Contains(body, "fake task") {
		t.Fatalf("watch did not send expected payload: %s", body)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "fake.last")); err != nil {
		t.Fatalf("state file missing: %v", err)
	}
}

func TestRegisterAndUnregisterCronFile(t *testing.T) {
	dir := t.TempDir()
	probe := filepath.Join(dir, "fake.conf")
	cronDir := filepath.Join(dir, "cron")
	if err := os.WriteFile(probe, []byte(`GROUP="idc-infra"`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "./task-progress", "register", probe, "--cron-dir", cronDir)
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("register failed: %v\n%s", err, out)
	}
	cronFile := filepath.Join(cronDir, "task-progress-fake")
	data, err := os.ReadFile(cronFile)
	if err != nil {
		t.Fatalf("cron file missing: %v", err)
	}
	if !strings.Contains(string(data), "*/30 * * * * root") {
		t.Fatalf("cron schedule missing: %s", data)
	}

	cmd = exec.Command("bash", "./task-progress", "unregister", probe, "--cron-dir", cronDir)
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("unregister failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(cronFile); !os.IsNotExist(err) {
		t.Fatalf("cron file still exists or stat failed: %v", err)
	}
}

func TestBtrfsDeviceDeleteTemplateParsesRemainingBytes(t *testing.T) {
	out := runBash(t, `
set -euo pipefail
TASK_PROGRESS_SOURCE_ONLY=1 . ./task-progress
DEVICE=/dev/sdf1
MOUNTPOINT=/mnt/bulk
. ./probes/btrfs-device-delete.conf
btrfs() {
  cat <<'EOF'
Label: none  uuid: abc
	Total devices 2 FS bytes used 4.00GiB
	devid    1 size 10.00GiB used 1.00GiB path /dev/sda1
	devid    2 size 10.00GiB used 2.00GiB path /dev/sdf1
EOF
}
remaining_bytes
`)
	if out != "2147483648" {
		t.Fatalf("unexpected btrfs remaining bytes: %s", out)
	}
}

func TestRsyncTemplateUsesDestinationBytes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "part.bin"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "part2.bin"), []byte("12"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := `
set -euo pipefail
TASK_PROGRESS_SOURCE_ONLY=1 . ./task-progress
TOTAL_BYTES=10
DEST_DIR=` + shellArg(dir) + `
. ./probes/rsync.conf
remaining_bytes
`
	out := runBash(t, script)
	if out != "3" {
		t.Fatalf("unexpected rsync remaining bytes: %s", out)
	}
}

func shellArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
