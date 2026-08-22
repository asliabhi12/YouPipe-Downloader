package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"ytd-local/internal/downloader"
	"ytd-local/internal/jobs"
)

const ServerVersion = "0.1.0"
const DefaultAddr = "127.0.0.1:47821"

type Server struct {
	addr       string
	dl         downloader.Downloader
	jm         *jobs.JobManager
	outputDir  string
	httpServer *http.Server
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
}

type MetadataRequest struct {
	URL string `json:"url"`
}

type DownloadRequest struct {
	URL     string `json:"url"`
	Quality string `json:"quality"`
}

type DownloadResponse struct {
	JobID string `json:"job_id"`
}

type CancelResponse struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}

func NewServer(addr string, dl downloader.Downloader, jm *jobs.JobManager, outputDir string) *Server {
	if addr == "" {
		addr = DefaultAddr
	}
	if outputDir == "" {
		var err error
		outputDir, err = downloader.GetDefaultDownloadsDir()
		if err != nil {
			log.Printf("Warning: failed to determine default downloads dir: %v", err)
			outputDir = "./downloads"
		}
	}

	s := &Server{
		addr:      addr,
		dl:        dl,
		jm:        jm,
		outputDir: outputDir,
	}

	mux := s.routes()
	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      s.corsMiddleware(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 2 * time.Minute,
		IdleTimeout:  60 * time.Second,
	}

	return s
}

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /metadata", s.handleMetadata)
	mux.HandleFunc("POST /downloads", s.handleCreateDownload)
	mux.HandleFunc("GET /downloads/{id}", s.handleGetDownload)
	mux.HandleFunc("POST /downloads/{id}/cancel", s.handleCancelDownload)

	return mux
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, errCode string, details string) {
	writeJSON(w, status, ErrorResponse{
		Error:   errCode,
		Details: details,
	})
}

// handleHealth answers "can this Helper actually complete a download?", not
// merely "is it running?".
//
// js_runtime_available is reported alongside the two older fields and folded into
// status. Folding it in is what makes the answer honest for clients that predate
// the field: a Helper with no JavaScript runtime is installed but cannot read a
// single YouTube page, and reporting status "ok" in that state is what previously
// let the website offer local downloads that could only ever fail.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	deps := s.dl.CheckDependencies()

	statusStr := "ok"
	if !deps.Ready() {
		statusStr = "degraded"
	}

	resp := map[string]interface{}{
		"status":               statusStr,
		"version":              ServerVersion,
		"ytdlp_available":      deps.Ytdlp,
		"ffmpeg_available":     deps.Ffmpeg,
		"js_runtime_available": deps.JSRuntime,
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleMetadata(w http.ResponseWriter, r *http.Request) {
	var req MetadataRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Failed to parse JSON body")
		return
	}

	if err := downloader.ValidateURL(req.URL); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_url", err.Error())
		return
	}

	meta, err := s.dl.Metadata(r.Context(), req.URL)
	if err != nil {
		if errors.Is(err, downloader.ErrYtdlpMissing) {
			writeError(w, http.StatusServiceUnavailable, "yt_dlp_missing", "yt-dlp executable not found")
			return
		}
		// A named reason beats letting yt-dlp exit 1 and calling it
		// metadata_failed: this one is a broken installation, not a video the
		// Helper could not read, and only the first of those is worth retrying
		// somewhere else.
		if errors.Is(err, downloader.ErrJSRuntimeMissing) {
			writeError(w, http.StatusServiceUnavailable, "js_runtime_missing", "bundled JavaScript runtime not found")
			return
		}
		if errors.Is(err, downloader.ErrInvalidURL) {
			writeError(w, http.StatusBadRequest, "invalid_url", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "metadata_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, meta)
}

func (s *Server) handleCreateDownload(w http.ResponseWriter, r *http.Request) {
	var req DownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Failed to parse JSON body")
		return
	}

	if err := downloader.ValidateURL(req.URL); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_url", err.Error())
		return
	}

	quality := strings.TrimSpace(req.Quality)
	if quality == "" {
		quality = "best"
	}

	job, jobCtx := s.jm.CreateJob(context.Background(), req.URL, quality)

	// Launch async download
	go s.runDownloadJob(jobCtx, job.ID, req.URL, quality)

	log.Printf("Started download job %s for URL: %s [quality: %s]", job.ID, req.URL, quality)

	writeJSON(w, http.StatusOK, DownloadResponse{
		JobID: job.ID,
	})
}

func (s *Server) runDownloadJob(ctx context.Context, jobID string, rawURL string, quality string) {
	err := s.dl.Download(ctx, rawURL, quality, s.outputDir, func(prog downloader.Progress) {
		s.jm.UpdateJobProgress(jobID, prog)
	})

	if err != nil {
		if errors.Is(err, downloader.ErrCancelled) || errors.Is(ctx.Err(), context.Canceled) {
			s.jm.CancelJob(jobID)
			log.Printf("Download job %s cancelled", jobID)
		} else {
			s.jm.SetJobFailed(jobID, err)
			log.Printf("Download job %s failed: %v", jobID, err)
		}
	} else {
		s.jm.SetJobCompleted(jobID)
		log.Printf("Download job %s completed successfully", jobID)
	}
}

func (s *Server) handleGetDownload(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if jobID == "" {
		writeError(w, http.StatusBadRequest, "missing_job_id", "Job ID is required")
		return
	}

	job, exists := s.jm.GetJob(jobID)
	if !exists {
		writeError(w, http.StatusNotFound, "job_not_found", fmt.Sprintf("Job %s not found", jobID))
		return
	}

	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleCancelDownload(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if jobID == "" {
		writeError(w, http.StatusBadRequest, "missing_job_id", "Job ID is required")
		return
	}

	cancelled := s.jm.CancelJob(jobID)
	if !cancelled {
		// Job might not exist or already be finished/cancelled
		job, exists := s.jm.GetJob(jobID)
		if !exists {
			writeError(w, http.StatusNotFound, "job_not_found", fmt.Sprintf("Job %s not found", jobID))
			return
		}
		writeJSON(w, http.StatusOK, CancelResponse{
			JobID:  jobID,
			Status: string(job.Status),
		})
		return
	}

	log.Printf("Download job %s cancelled by user request", jobID)

	writeJSON(w, http.StatusOK, CancelResponse{
		JobID:  jobID,
		Status: "cancelled",
	})
}

func (s *Server) Start() error {
	// Verify host binding is 127.0.0.1
	host, _, err := net.SplitHostPort(s.addr)
	if err != nil || (host != "127.0.0.1" && host != "localhost") {
		return fmt.Errorf("security error: server must bind to 127.0.0.1 (got %s)", s.addr)
	}

	// Bind before announcing it. Logging first would put a "listening" line in
	// the log file even when the port is already taken, which is the one case
	// where somebody is actually reading the log to find out what went wrong.
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}

	log.Printf("YouPiper Helper listening on http://%s", s.addr)
	log.Printf("Default download directory: %s", s.outputDir)
	return s.httpServer.Serve(ln)
}

func (s *Server) Shutdown(ctx context.Context) error {
	log.Println("Shutting down YouPiper Helper...")
	s.jm.CancelAll()
	return s.httpServer.Shutdown(ctx)
}
