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
	Ytdlp  bool
	Ffmpeg bool
}

func (m *MockDownloader) Metadata(ctx context.Context, rawURL string) (*downloader.Metadata, error) {
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

func (m *MockDownloader) CheckDependencies() (bool, bool) {
	return m.Ytdlp, m.Ffmpeg
}

func setupTestServer() (*Server, *MockDownloader, *jobs.JobManager) {
	mockDL := &MockDownloader{Ytdlp: true, Ffmpeg: true}
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
