//go:build !windows

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withFakeYtDlp writes a fake yt-dlp shell script into a temp dir and prepends
// that dir to PATH for the duration of the test. The script body runs with the
// same working directory yt-dlp would be invoked from (cmd.Dir = tmpDir), so it
// can drop fake .srt files there.
func withFakeYtDlp(t *testing.T, script string) {
	t.Helper()
	binDir := t.TempDir()
	path := filepath.Join(binDir, "yt-dlp")
	body := "#!/bin/sh\n" + script + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake yt-dlp: %v", err)
	}
	orig := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+orig)
}

func doPost(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handleGenerate(rr, req)
	return rr
}

func TestHandleGenerate_InvalidJSON(t *testing.T) {
	rr := doPost(t, "not json")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusBadRequest)
	}
	var er errorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &er); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if !strings.Contains(er.Error, "invalid json") {
		t.Errorf("error message: got %q, want it to mention 'invalid json'", er.Error)
	}
}

func TestHandleGenerate_MissingData(t *testing.T) {
	rr := doPost(t, `{}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusBadRequest)
	}
	var er errorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &er); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if !strings.Contains(er.Error, "missing 'data'") {
		t.Errorf("error message: got %q, want it to mention missing data", er.Error)
	}
}

func TestHandleGenerate_Success(t *testing.T) {
	// Fake yt-dlp drops a single .srt in the current working directory (which
	// the handler sets via cmd.Dir).
	srt := "1\n00:00:00,000 --> 00:00:01,000\nhello\n"
	withFakeYtDlp(t, `cat > vid.en.srt <<'EOF'
`+srt+`EOF`)

	rr := doPost(t, `{"data":"https://example.test/video"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type: got %q, want application/json", ct)
	}
	var resp response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Srt != srt {
		t.Errorf("srt body mismatch:\n got %q\nwant %q", resp.Srt, srt)
	}
}

func TestHandleGenerate_YtDlpFails(t *testing.T) {
	withFakeYtDlp(t, `echo "boom" >&2; exit 2`)

	rr := doPost(t, `{"data":"https://example.test/video"}`)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500; body=%s", rr.Code, rr.Body.String())
	}
	var er errorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &er); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if !strings.Contains(er.Error, "yt-dlp failed") {
		t.Errorf("error message: got %q, want it to mention 'yt-dlp failed'", er.Error)
	}
	if !strings.Contains(er.Error, "boom") {
		t.Errorf("error message: got %q, want it to include stderr 'boom'", er.Error)
	}
}

func TestHandleGenerate_NoSrtProduced(t *testing.T) {
	// yt-dlp succeeds but writes no .srt file.
	withFakeYtDlp(t, `echo "nothing to do"; exit 0`)

	rr := doPost(t, `{"data":"https://example.test/video"}`)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500; body=%s", rr.Code, rr.Body.String())
	}
	var er errorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &er); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if !strings.Contains(er.Error, "no srt produced") {
		t.Errorf("error message: got %q, want it to mention 'no srt produced'", er.Error)
	}
}

func TestHandleGenerate_PassesURLToYtDlp(t *testing.T) {
	// Fake yt-dlp records its argv into a file we can inspect, then writes a
	// minimal srt so the handler returns 200.
	argLog := filepath.Join(t.TempDir(), "args")
	t.Setenv("YTDLP_ARG_LOG", argLog)
	withFakeYtDlp(t, `printf '%s\n' "$@" > "$YTDLP_ARG_LOG"
printf '1\n00:00:00,000 --> 00:00:01,000\nx\n' > vid.en.srt`)

	url := "https://example.test/watch?v=abc123"
	rr := doPost(t, `{"data":"`+url+`"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	b, err := os.ReadFile(argLog)
	if err != nil {
		t.Fatalf("read arg log: %v", err)
	}
	args := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(args) == 0 || args[len(args)-1] != url {
		t.Errorf("yt-dlp args: got %v, want last arg to be %q", args, url)
	}

	want := map[string]bool{
		"--skip-download":   false,
		"--write-auto-subs": false,
		"--convert-subs":    false,
	}
	for _, a := range args {
		if _, ok := want[a]; ok {
			want[a] = true
		}
	}
	for flag, seen := range want {
		if !seen {
			t.Errorf("yt-dlp args missing %q; got %v", flag, args)
		}
	}
}
