package jobs

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"ytd-local/internal/downloader"
)

func TestJobCreationAndLookup(t *testing.T) {
	jm := NewJobManager()
	ctx := context.Background()

	job, _ := jm.CreateJob(ctx, "https://youtube.com/watch?v=test", "1080p")
	if job.ID == "" {
		t.Fatal("Expected non-empty job ID")
	}
	if job.Status != StatusQueued {
		t.Errorf("Expected status %s, got %s", StatusQueued, job.Status)
	}

	fetched, exists := jm.GetJob(job.ID)
	if !exists {
		t.Fatalf("Expected job %s to exist", job.ID)
	}
	if fetched.URL != job.URL || fetched.Quality != job.Quality {
		t.Errorf("Job fields mismatch. Got %+v, want %+v", fetched, job)
	}
}

func TestJobStateTransitions(t *testing.T) {
	jm := NewJobManager()
	ctx := context.Background()

	job, _ := jm.CreateJob(ctx, "https://youtube.com/watch?v=test", "720p")

	// Transition: downloading
	jm.UpdateJobProgress(job.ID, downloader.Progress{
		Status:          "downloading",
		Progress:        45.5,
		Speed:           1024000,
		ETA:             10,
		DownloadedBytes: 45500,
		TotalBytes:      100000,
	})

	updated, _ := jm.GetJob(job.ID)
	if updated.Status != StatusDownloading {
		t.Errorf("Expected status %s, got %s", StatusDownloading, updated.Status)
	}
	if updated.Progress != 45.5 {
		t.Errorf("Expected progress 45.5, got %f", updated.Progress)
	}

	// Transition: processing
	jm.UpdateJobProgress(job.ID, downloader.Progress{
		Status:   "processing",
		Progress: 99.0,
	})
	updated, _ = jm.GetJob(job.ID)
	if updated.Status != StatusProcessing {
		t.Errorf("Expected status %s, got %s", StatusProcessing, updated.Status)
	}

	// Transition: completed
	jm.SetJobCompleted(job.ID)
	updated, _ = jm.GetJob(job.ID)
	if updated.Status != StatusCompleted {
		t.Errorf("Expected status %s, got %s", StatusCompleted, updated.Status)
	}
	if updated.Progress != 100.0 {
		t.Errorf("Expected progress 100.0, got %f", updated.Progress)
	}
}

func TestJobCancellation(t *testing.T) {
	jm := NewJobManager()
	ctx := context.Background()

	job, jobCtx := jm.CreateJob(ctx, "https://youtube.com/watch?v=test", "best")

	cancelled := jm.CancelJob(job.ID)
	if !cancelled {
		t.Fatal("Expected CancelJob to return true")
	}

	select {
	case <-jobCtx.Done():
		// Success: context was cancelled
	case <-time.After(1 * time.Second):
		t.Fatal("Expected job context to be cancelled")
	}

	updated, _ := jm.GetJob(job.ID)
	if updated.Status != StatusCancelled {
		t.Errorf("Expected status %s, got %s", StatusCancelled, updated.Status)
	}

	// Cancelling again should return false
	if jm.CancelJob(job.ID) {
		t.Error("Expected second CancelJob call to return false")
	}
}

func TestJobFailure(t *testing.T) {
	jm := NewJobManager()
	ctx := context.Background()

	job, _ := jm.CreateJob(ctx, "https://youtube.com/watch?v=test", "360p")

	testErr := fmt.Errorf("download failed: network error")
	jm.SetJobFailed(job.ID, testErr)

	updated, _ := jm.GetJob(job.ID)
	if updated.Status != StatusFailed {
		t.Errorf("Expected status %s, got %s", StatusFailed, updated.Status)
	}
	if updated.Error != testErr.Error() {
		t.Errorf("Expected error %q, got %q", testErr.Error(), updated.Error)
	}
}

func TestConcurrentJobManagerAccess(t *testing.T) {
	jm := NewJobManager()
	ctx := context.Background()

	var wg sync.WaitGroup
	numWorkers := 20
	jobsPerWorker := 10

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < jobsPerWorker; j++ {
				url := fmt.Sprintf("https://youtube.com/watch?v=test_%d_%d", workerID, j)
				job, _ := jm.CreateJob(ctx, url, "720p")

				jm.UpdateJobProgress(job.ID, downloader.Progress{
					Status:   "downloading",
					Progress: 50.0,
				})

				_, exists := jm.GetJob(job.ID)
				if !exists {
					t.Errorf("Failed to find job %s", job.ID)
				}

				if j%2 == 0 {
					jm.CancelJob(job.ID)
				} else {
					jm.SetJobCompleted(job.ID)
				}
			}
		}(i)
	}

	wg.Wait()
}
