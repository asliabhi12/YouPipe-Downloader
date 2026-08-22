package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ytd-local/internal/downloader"
	"ytd-local/internal/jobs"
)

type MockDownloader struct {
	Ytdlp     bool
	Ffmpeg    bool
	JSRuntime bool
	// MetadataErr, when set, is returned by Metadata instead of a result. Used to
	// drive the "installation is broken" responses.
	MetadataErr error
}

func (m *MockDownloader) Metadata(ctx context.Context, rawURL string) (*downloader.Metadata, error) {
	if m.MetadataErr != nil {
		return nil, m.MetadataErr
	}
	if rawURL == "https://invalid.com/error" {
		return nil, downloader.ErrMetadataFailed
	}
	return &downloader.Metadata{
		ID:        "VIDEO_ID",
		Title:     "Test Video Title",
		Thumbnail: "https://img.youtube.com/vi/VIDEO_ID/hqdefault.jpg",
		Duration:  120.0,
		Uploader:  "Test Channel",
		Formats: []downloader.Format{
			{Quality: "1080p", Height: 1080},
			{Quality: "720p", Height: 720},
		},
	}, nil
}

func (m *MockDownloader) Download(ctx context.Context, rawURL string, quality string, outputDir string, progressCb func(downloader.Progress)) error {
	if progressCb != nil {
		progressCb(downloader.Progress{
			Status:   "downloading",
			Progress: 50.0,
		})
	}
	select {
	case <-ctx.Done():
		return downloader.ErrCancelled
	case <-time.After(10 * time.Millisecond):
	}
	if progressCb != nil {
		progressCb(downloader.Progress{
			Status:   "completed",
			Progress: 100.0,
		})
	}
	return nil
}

func (m *MockDownloader) CheckDependencies() downloader.Dependencies {
	return downloader.Dependencies{
		Ytdlp:     m.Ytdlp,
		Ffmpeg:    m.Ffmpeg,
		JSRuntime: m.JSRuntime,
	}
}

func setupTestServer() (*Server, *MockDownloader, *jobs.JobManager) {
	mockDL := &MockDownloader{Ytdlp: true, Ffmpeg: true, JSRuntime: true}
	jm := jobs.NewJobManager()
	srv := NewServer("127.0.0.1:47821", mockDL, jm, "/tmp/ytd-test")
	return srv, mockDL, jm
}

func TestHealthEndpoint(t *testing.T) {
	srv, _, _ := setupTestServer()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status code %d, got %d", http.StatusOK, rec.Code)
	}

	var res map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("Failed to decode response JSON: %v", err)
	}

	if res["status"] != "ok" {
		t.Errorf("Expected status 'ok', got %v", res["status"])
	}
	if res["version"] != ServerVersion {
		t.Errorf("Expected version %s, got %v", ServerVersion, res["version"])
	}
}

func TestCORSHeaders(t *testing.T) {
	srv, _, _ := setupTestServer()

	req := httptest.NewRequest(http.MethodOptions, "/health", nil)
	rec := httptest.NewRecorder()

	srv.corsMiddleware(srv.routes()).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("Expected status code %d for OPTIONS, got %d", http.StatusNoContent, rec.Code)
	}

	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("Expected CORS Access-Control-Allow-Origin header to be *")
	}
}

func TestMetadataEndpoint(t *testing.T) {
	srv, _, _ := setupTestServer()

	body := []byte(`{"url": "https://www.youtube.com/watch?v=VIDEO_ID"}`)
	req := httptest.NewRequest(http.MethodPost, "/metadata", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status code %d, got %d", http.StatusOK, rec.Code)
	}

	var meta downloader.Metadata
	if err := json.NewDecoder(rec.Body).Decode(&meta); err != nil {
		t.Fatalf("Failed to decode metadata response: %v", err)
	}

	if meta.ID != "VIDEO_ID" || meta.Title != "Test Video Title" {
		t.Errorf("Unexpected metadata content: %+v", meta)
	}
}

func TestCreateDownloadAndGetProgress(t *testing.T) {
	srv, _, _ := setupTestServer()

	body := []byte(`{"url": "https://www.youtube.com/watch?v=VIDEO_ID", "quality": "1080p"}`)
	req := httptest.NewRequest(http.MethodPost, "/downloads", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status code %d, got %d", http.StatusOK, rec.Code)
	}

	var res DownloadResponse
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("Failed to decode download response: %v", err)
	}

	if res.JobID == "" {
		t.Fatal("Expected non-empty job_id in download response")
	}

	// Fetch job status
	getReq := httptest.NewRequest(http.MethodGet, "/downloads/"+res.JobID, nil)
	getRec := httptest.NewRecorder()

	srv.routes().ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("Expected status code %d on GET /downloads/{id}, got %d", http.StatusOK, getRec.Code)
	}

	var job jobs.Job
	if err := json.NewDecoder(getRec.Body).Decode(&job); err != nil {
		t.Fatalf("Failed to decode job status: %v", err)
	}

	if job.ID != res.JobID {
		t.Errorf("Expected job ID %s, got %s", res.JobID, job.ID)
	}
}

func TestCancelDownloadEndpoint(t *testing.T) {
	srv, _, _ := setupTestServer()

	// Create job first
	body := []byte(`{"url": "https://www.youtube.com/watch?v=VIDEO_ID", "quality": "720p"}`)
	req := httptest.NewRequest(http.MethodPost, "/downloads", bytes.NewBuffer(body))
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	var res DownloadResponse
	_ = json.NewDecoder(rec.Body).Decode(&res)

	// Cancel job
	cancelReq := httptest.NewRequest(http.MethodPost, "/downloads/"+res.JobID+"/cancel", nil)
	cancelRec := httptest.NewRecorder()
	srv.routes().ServeHTTP(cancelRec, cancelReq)

	if cancelRec.Code != http.StatusOK {
		t.Fatalf("Expected status code %d on cancel, got %d", http.StatusOK, cancelRec.Code)
	}

	var cancelRes CancelResponse
	_ = json.NewDecoder(cancelRec.Body).Decode(&cancelRes)

	if cancelRes.JobID != res.JobID || cancelRes.Status != "cancelled" {
		t.Errorf("Unexpected cancel response: %+v", cancelRes)
	}
}

// --- JavaScript runtime readiness -------------------------------------------

// healthBody runs GET /health against a Helper with the given dependency state.
func healthBody(t *testing.T, deps MockDownloader) map[string]interface{} {
	t.Helper()
	srv := NewServer("127.0.0.1:47821", &deps, jobs.NewJobManager(), "/tmp/ytd-test")

	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/health status = %d, want %d", rec.Code, http.StatusOK)
	}

	var res map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("decoding /health: %v", err)
	}
	return res
}

// REG-JS-004: /health reports whether the JavaScript runtime is available, so
// "installed" and "able to download" can be told apart from outside.
func TestHealthReportsJSRuntimeAvailability(t *testing.T) {
	res := healthBody(t, MockDownloader{Ytdlp: true, Ffmpeg: true, JSRuntime: true})

	if res["js_runtime_available"] != true {
		t.Errorf("js_runtime_available = %v, want true", res["js_runtime_available"])
	}
	if res["status"] != "ok" {
		t.Errorf("status = %v, want ok", res["status"])
	}
}

// REG-JS-005: a Helper with yt-dlp and FFmpeg but no runtime is degraded, not
// ok. This is the case the old health check called ok — the Helper was running,
// answering, and unable to read a single video.
func TestHealthDegradedWithoutJSRuntime(t *testing.T) {
	res := healthBody(t, MockDownloader{Ytdlp: true, Ffmpeg: true, JSRuntime: false})

	if res["status"] != "degraded" {
		t.Errorf("status = %v, want degraded", res["status"])
	}
	if res["js_runtime_available"] != false {
		t.Errorf("js_runtime_available = %v, want false", res["js_runtime_available"])
	}
	// The older fields keep their own meaning. A client that only knows about
	// those two still sees a non-ok status, which is what stops it offering local
	// downloads that cannot work.
	if res["ytdlp_available"] != true || res["ffmpeg_available"] != true {
		t.Errorf("legacy fields changed meaning: ytdlp=%v ffmpeg=%v",
			res["ytdlp_available"], res["ffmpeg_available"])
	}
}

// The field is additive: everything a client read before is still there, so
// nothing that predates the runtime check breaks.
func TestHealthKeepsExistingFields(t *testing.T) {
	res := healthBody(t, MockDownloader{Ytdlp: true, Ffmpeg: true, JSRuntime: true})
	for _, key := range []string{"status", "version", "ytdlp_available", "ffmpeg_available"} {
		if _, ok := res[key]; !ok {
			t.Errorf("/health no longer reports %q", key)
		}
	}
	if res["version"] != ServerVersion {
		t.Errorf("version = %v, want %s", res["version"], ServerVersion)
	}
}

func TestHealthDegradedWhenEveryToolMissing(t *testing.T) {
	res := healthBody(t, MockDownloader{})
	if res["status"] != "degraded" {
		t.Errorf("status = %v, want degraded", res["status"])
	}
}

// A missing runtime is a broken installation, not a video that could not be
// read. Saying so distinguishes it from metadata_failed, which is what any
// caller needs in order to decide whether retrying elsewhere is worth it.
func TestMetadataReportsMissingJSRuntime(t *testing.T) {
	mock := &MockDownloader{Ytdlp: true, Ffmpeg: true, MetadataErr: downloader.ErrJSRuntimeMissing}
	srv := NewServer("127.0.0.1:47821", mock, jobs.NewJobManager(), "/tmp/ytd-test")

	body := bytes.NewBufferString(`{"url":"https://www.youtube.com/watch?v=x"}`)
	req := httptest.NewRequest(http.MethodPost, "/metadata", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	var res ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if res.Error != "js_runtime_missing" {
		t.Fatalf("error = %q, want js_runtime_missing", res.Error)
	}
}
