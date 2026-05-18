package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

func writeError(w http.ResponseWriter, status int, msg string) {
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
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "srt"
	}
	if format != "srt" && format != "txt" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid format %q: must be 'srt' or 'txt'", format))
		return
	}

	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid json: %v", err))
		return
	}
	if req.Data == "" {
		writeError(w, http.StatusBadRequest, "missing 'data' field")
		return
	}

	tmpDir, err := os.MkdirTemp("", "ytsrtgen-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("mktemp: %v", err))
		return
	}
	defer os.RemoveAll(tmpDir)

	cmd := exec.CommandContext(r.Context(),
		"yt-dlp",
		"--skip-download",
		"--write-auto-subs",
		"--sub-lang", "en",
		"--sub-format", "srt",
		"--convert-subs", "srt",
		"-o", "%(id)s.%(ext)s",
		req.Data,
	)
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("yt-dlp failed: %v: %s", err, output))
		return
	}

	matches, err := filepath.Glob(filepath.Join(tmpDir, "*.srt"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("glob: %v", err))
		return
	}
	if len(matches) == 0 {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("no srt produced; yt-dlp output: %s", output))
		return
	}

	srt, err := os.ReadFile(matches[0])
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("read srt: %v", err))
		return
	}

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

	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write([]byte(body))
}

func main() {
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	r := mux.NewRouter()
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
