package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	httpSwagger "github.com/swaggo/http-swagger"

	_ "zephinzer/ytsrtgen/docs"
)

// @title           ytsrtgen API
// @version         1.0
// @description     Fetches auto-generated English subtitles for a video URL via yt-dlp.
// @BasePath        /

type request struct {
	// Data is the video URL to fetch subtitles for.
	Data string `json:"data" example:"https://www.youtube.com/watch?v=dQw4w9WgXcQ"`
}

type errorResponse struct {
	Error string `json:"error"`
}

type ctxKey string

const ctxKeyReqID ctxKey = "reqID"

var reqCounter uint64

func nextReqID() string {
	return fmt.Sprintf("req-%06d", atomic.AddUint64(&reqCounter, 1))
}

func reqIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyReqID).(string); ok {
		return v
	}
	return "req-?"
}

func logf(ctx context.Context, format string, args ...any) {
	log.Printf("[%s] "+format, append([]any{reqIDFrom(ctx)}, args...)...)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := nextReqID()
		ctx := context.WithValue(r.Context(), ctxKeyReqID, id)
		r = r.WithContext(ctx)

		start := time.Now()
		logf(ctx, "request start method=%s path=%s remote=%s ua=%q query=%q",
			r.Method, r.URL.Path, r.RemoteAddr, r.UserAgent(), r.URL.RawQuery)

		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		logf(ctx, "request end status=%d bytes=%d duration=%s",
			rec.status, rec.bytes, time.Since(start))
	})
}

func writeError(ctx context.Context, w http.ResponseWriter, status int, msg string) {
	logf(ctx, "error status=%d msg=%s", status, msg)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: msg})
}

// srtToText strips sequence numbers and timestamp lines from SRT content,
// returning only the cue text joined by newlines.
func srtToText(srt string) string {
	s := strings.ReplaceAll(srt, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	var out strings.Builder
	for block := range strings.SplitSeq(s, "\n\n") {
		lines := strings.Split(block, "\n")
		for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
			lines = lines[1:]
		}
		if len(lines) < 2 || !strings.Contains(lines[1], "-->") {
			continue
		}
		for _, t := range lines[2:] {
			if strings.TrimSpace(t) == "" {
				continue
			}
			out.WriteString(t)
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// handleGenerate godoc
// @Summary      Generate subtitles for a video URL
// @Description  Runs yt-dlp against the supplied URL and returns the English subtitles in the requested format.
// @Accept       json
// @Produce      plain
// @Param        format  query     string   false  "Output format"  Enums(srt, txt)  default(srt)
// @Param        body    body      request  true   "Video URL"
// @Success      200     {string}  string   "Subtitle content in the requested format"
// @Failure      400     {object}  errorResponse
// @Failure      500     {object}  errorResponse
// @Router       / [post]
func handleGenerate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "srt"
	}
	if format != "srt" && format != "txt" {
		writeError(ctx, w, http.StatusBadRequest, fmt.Sprintf("invalid format %q: must be 'srt' or 'txt'", format))
		return
	}
	logf(ctx, "format=%s", format)

	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(ctx, w, http.StatusBadRequest, fmt.Sprintf("invalid json: %v", err))
		return
	}
	if req.Data == "" {
		writeError(ctx, w, http.StatusBadRequest, "missing 'data' field")
		return
	}
	logf(ctx, "url=%s", req.Data)

	tmpDir, err := os.MkdirTemp("", "ytsrtgen-*")
	if err != nil {
		writeError(ctx, w, http.StatusInternalServerError, fmt.Sprintf("mktemp: %v", err))
		return
	}
	defer os.RemoveAll(tmpDir)
	logf(ctx, "tmpdir=%s", tmpDir)

	args := []string{
		"--verbose",
		"--remote-components", "ejs:github",
		"--skip-download",
		"--write-auto-subs",
		"--sub-lang", "en",
		"--sub-format", "srt",
		"--convert-subs", "srt",
		"-o", "%(id)s.%(ext)s",
		req.Data,
	}
	logf(ctx, "exec yt-dlp args=%q", args)

	ytStart := time.Now()
	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	logf(ctx, "yt-dlp finished duration=%s exit_err=%v", time.Since(ytStart), err)
	for line := range strings.SplitSeq(strings.TrimRight(string(output), "\n"), "\n") {
		logf(ctx, "yt-dlp| %s", line)
	}
	if err != nil {
		writeError(ctx, w, http.StatusInternalServerError, fmt.Sprintf("yt-dlp failed: %v: %s", err, output))
		return
	}

	matches, err := filepath.Glob(filepath.Join(tmpDir, "*.srt"))
	if err != nil {
		writeError(ctx, w, http.StatusInternalServerError, fmt.Sprintf("glob: %v", err))
		return
	}
	if len(matches) == 0 {
		writeError(ctx, w, http.StatusInternalServerError, fmt.Sprintf("no srt produced; yt-dlp output: %s", output))
		return
	}
	logf(ctx, "srt matches=%v", matches)

	srt, err := os.ReadFile(matches[0])
	if err != nil {
		writeError(ctx, w, http.StatusInternalServerError, fmt.Sprintf("read srt: %v", err))
		return
	}
	logf(ctx, "srt bytes=%d source=%s", len(srt), matches[0])

	var body string
	var contentType string
	switch format {
	case "txt":
		body = srtToText(string(srt))
		contentType = "text/plain; charset=utf-8"
	default:
		body = string(srt)
		contentType = "application/x-subrip; charset=utf-8"
	}
	logf(ctx, "response format=%s content_type=%s bytes=%d", format, contentType, len(body))

	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write([]byte(body))
}

func main() {
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	r := mux.NewRouter()
	r.Use(loggingMiddleware)
	r.HandleFunc("/", handleGenerate).Methods(http.MethodPost)
	r.PathPrefix("/swagger/").Handler(httpSwagger.WrapHandler)
	r.HandleFunc("/swagger", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/swagger/index.html", http.StatusFound)
	})

	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatal(err)
	}
}
