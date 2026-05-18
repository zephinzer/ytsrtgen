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

func doPost(t *testing.T, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	rr := httptest.NewRecorder()
	handleGenerate(rr, req)
	return rr
}

const sampleSrt = "1\n00:00:00,000 --> 00:00:01,000\nhello\nworld\n\n2\n00:00:01,000 --> 00:00:02,000\ngoodbye\n"

func writeSampleSrtScript() string {
	return `cat > vid.en.srt <<'EOF'
` + sampleSrt + `EOF`
}

func TestHandleGenerate_InvalidJSON(t *testing.T) {
	rr := doPost(t, "/", "not json")
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
	rr := doPost(t, "/", `{}`)
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

func TestHandleGenerate_InvalidFormat(t *testing.T) {
	rr := doPost(t, "/?format=json", `{"data":"https://example.test/video"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusBadRequest)
	}
	var er errorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &er); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if !strings.Contains(er.Error, "invalid format") {
		t.Errorf("error message: got %q, want it to mention 'invalid format'", er.Error)
	}
}

func TestHandleGenerate_DefaultFormatIsSrt(t *testing.T) {
	withFakeYtDlp(t, writeSampleSrtScript())

	rr := doPost(t, "/", `{"data":"https://example.test/video"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/x-subrip; charset=utf-8" {
		t.Errorf("content-type: got %q, want application/x-subrip; charset=utf-8", ct)
	}
	if rr.Body.String() != sampleSrt {
		t.Errorf("srt body mismatch:\n got %q\nwant %q", rr.Body.String(), sampleSrt)
	}
}

func TestHandleGenerate_FormatSrt(t *testing.T) {
	withFakeYtDlp(t, writeSampleSrtScript())

	rr := doPost(t, "/?format=srt", `{"data":"https://example.test/video"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/x-subrip; charset=utf-8" {
		t.Errorf("content-type: got %q, want application/x-subrip; charset=utf-8", ct)
	}
	if rr.Body.String() != sampleSrt {
		t.Errorf("srt body mismatch:\n got %q\nwant %q", rr.Body.String(), sampleSrt)
	}
}

func TestHandleGenerate_FormatTxt(t *testing.T) {
	withFakeYtDlp(t, writeSampleSrtScript())

	rr := doPost(t, "/?format=txt", `{"data":"https://example.test/video"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("content-type: got %q, want text/plain; charset=utf-8", ct)
	}
	want := "hello\nworld\ngoodbye\n"
	if rr.Body.String() != want {
		t.Errorf("txt body mismatch:\n got %q\nwant %q", rr.Body.String(), want)
	}
}

func TestHandleGenerate_YtDlpFails(t *testing.T) {
	withFakeYtDlp(t, `echo "boom" >&2; exit 2`)

	rr := doPost(t, "/", `{"data":"https://example.test/video"}`)
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
	withFakeYtDlp(t, `echo "nothing to do"; exit 0`)

	rr := doPost(t, "/", `{"data":"https://example.test/video"}`)
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
	argLog := filepath.Join(t.TempDir(), "args")
	t.Setenv("YTDLP_ARG_LOG", argLog)
	withFakeYtDlp(t, `printf '%s\n' "$@" > "$YTDLP_ARG_LOG"
printf '1\n00:00:00,000 --> 00:00:01,000\nx\n' > vid.en.srt`)

	url := "https://example.test/watch?v=abc123"
	rr := doPost(t, "/", `{"data":"`+url+`"}`)
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

func TestSrtToText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "basic",
			in:   "1\n00:00:00,000 --> 00:00:01,000\nhello\nworld\n\n2\n00:00:01,000 --> 00:00:02,000\nbye\n",
			want: "hello\nworld\nbye\n",
		},
		{
			name: "crlf line endings",
			in:   "1\r\n00:00:00,000 --> 00:00:01,000\r\nhello\r\n\r\n2\r\n00:00:01,000 --> 00:00:02,000\r\nbye\r\n",
			want: "hello\nbye\n",
		},
		{
			name: "empty input",
			in:   "",
			want: "",
		},
		{
			name: "malformed block skipped",
			in:   "garbage\n\n1\n00:00:00,000 --> 00:00:01,000\nok\n",
			want: "ok\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := srtToText(tc.in)
			if got != tc.want {
				t.Errorf("srtToText:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}
