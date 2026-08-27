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
	jm.Stop()
}

func TestJobExpirationCleanup(t *testing.T) {
	jm := NewJobManager()
	defer jm.Stop()
	ctx := context.Background()

	// Active jobs
	queuedJob, _ := jm.CreateJob(ctx, "https://youtube.com/watch?v=queued", "1080p")
	dlJob, _ := jm.CreateJob(ctx, "https://youtube.com/watch?v=downloading", "720p")
	jm.UpdateJobProgress(dlJob.ID, downloader.Progress{Status: "downloading", Progress: 30})

	procJob, _ := jm.CreateJob(ctx, "https://youtube.com/watch?v=processing", "480p")
	jm.UpdateJobProgress(procJob.ID, downloader.Progress{Status: "processing", Progress: 99})

	// Terminal jobs
	completedJob, _ := jm.CreateJob(ctx, "https://youtube.com/watch?v=completed", "1080p")
	jm.SetJobCompleted(completedJob.ID)

	failedJob, _ := jm.CreateJob(ctx, "https://youtube.com/watch?v=failed", "720p")
	jm.SetJobFailed(failedJob.ID, fmt.Errorf("network failure"))

	cancelledJob, _ := jm.CreateJob(ctx, "https://youtube.com/watch?v=cancelled", "360p")
	jm.CancelJob(cancelledJob.ID)

	// Artificially age the finishedAt timestamp of terminal jobs back 35 minutes
	pastTime := time.Now().Add(-35 * time.Minute)
	jm.mu.Lock()
	if item, ok := jm.jobs[completedJob.ID]; ok {
		item.finishedAt = pastTime
	}
	if item, ok := jm.jobs[failedJob.ID]; ok {
		item.finishedAt = pastTime
	}
	if item, ok := jm.jobs[cancelledJob.ID]; ok {
		item.finishedAt = pastTime
	}
	jm.mu.Unlock()

	// Perform cleanup with 30-minute TTL
	removed := jm.CleanupStale(30 * time.Minute)
	if removed != 3 {
		t.Errorf("Expected 3 jobs to be removed by CleanupStale, got %d", removed)
	}

	// Active jobs MUST be retained
	for _, id := range []string{queuedJob.ID, dlJob.ID, procJob.ID} {
		if _, exists := jm.GetJob(id); !exists {
			t.Errorf("Active job %s was incorrectly cleaned up", id)
		}
	}

	// Terminal jobs MUST be expired
	for _, id := range []string{completedJob.ID, failedJob.ID, cancelledJob.ID} {
		if _, exists := jm.GetJob(id); exists {
			t.Errorf("Expired terminal job %s was not cleaned up", id)
		}
	}
}

func TestJobManagerStopLifecycle(t *testing.T) {
	jm := NewJobManager()
	// Calling Stop should cleanly shut down the cleanup goroutine
	jm.Stop()
	// Second Stop call should be idempotent and not panic
	jm.Stop()
}

