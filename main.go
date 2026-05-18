package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/gorilla/mux"
	httpSwagger "github.com/swaggo/http-swagger"

	_ "zephinzer/ytsrtgen/docs"
)

// @title           ytsrtgen API
// @version         1.0
// @description     Fetches auto-generated English SRT subtitles for a video URL via yt-dlp.
// @BasePath        /

type request struct {
	// Data is the video URL to fetch subtitles for.
	Data string `json:"data" example:"https://www.youtube.com/watch?v=dQw4w9WgXcQ"`
}

type response struct {
	// Srt is the SRT-formatted subtitle content.
	Srt string `json:"srt"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: msg})
}

// handleGenerate godoc
// @Summary      Generate SRT for a video URL
// @Description  Runs yt-dlp against the supplied URL and returns the resulting English SRT subtitles.
// @Accept       json
// @Produce      json
// @Param        body  body      request        true  "Video URL"
// @Success      200   {object}  response
// @Failure      400   {object}  errorResponse
// @Failure      500   {object}  errorResponse
// @Router       / [post]
func handleGenerate(w http.ResponseWriter, r *http.Request) {
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

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response{Srt: string(srt)})
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
