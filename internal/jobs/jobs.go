package jobs

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	"ytd-local/internal/downloader"
)

type JobStatus string

const (
	StatusQueued      JobStatus = "queued"
	StatusDownloading JobStatus = "downloading"
	StatusProcessing  JobStatus = "processing"
	StatusCompleted   JobStatus = "completed"
	StatusFailed      JobStatus = "failed"
	StatusCancelled   JobStatus = "cancelled"
)

type Job struct {
	ID        string    `json:"job_id"`
	URL       string    `json:"url"`
	Quality   string    `json:"quality"`
	Status    JobStatus `json:"status"`
	Progress  float64   `json:"progress"`
	Speed     int64     `json:"speed"`
	ETA       int64     `json:"eta"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type jobItem struct {
	job    Job
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.RWMutex
}

type JobManager struct {
	jobs map[string]*jobItem
	mu   sync.RWMutex
}

func NewJobManager() *JobManager {
	return &JobManager{
		jobs: make(map[string]*jobItem),
	}
}

func generateUUID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant RFC 4122
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (jm *JobManager) CreateJob(parentCtx context.Context, url, quality string) (*Job, context.Context) {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	id := generateUUID()
	ctx, cancel := context.WithCancel(parentCtx)

	item := &jobItem{
		job: Job{
			ID:        id,
			URL:       url,
			Quality:   quality,
			Status:    StatusQueued,
			Progress:  0.0,
			Speed:     0,
			ETA:       0,
			CreatedAt: time.Now(),
		},
		ctx:    ctx,
		cancel: cancel,
	}

	jm.jobs[id] = item
	cp := item.job
	return &cp, ctx
}

func (jm *JobManager) GetJob(id string) (*Job, bool) {
	jm.mu.RLock()
	item, exists := jm.jobs[id]
	jm.mu.RUnlock()

	if !exists {
		return nil, false
	}

	item.mu.RLock()
	defer item.mu.RUnlock()

	cp := item.job
	return &cp, true
}

func (jm *JobManager) UpdateJobProgress(id string, prog downloader.Progress) {
	jm.mu.RLock()
	item, exists := jm.jobs[id]
	jm.mu.RUnlock()

	if !exists {
		return
	}

	item.mu.Lock()
	defer item.mu.Unlock()

	if item.job.Status == StatusCancelled || item.job.Status == StatusFailed {
		return
	}

	switch prog.Status {
	case "downloading":
		item.job.Status = StatusDownloading
	case "processing":
		item.job.Status = StatusProcessing
	case "completed":
		item.job.Status = StatusCompleted
	case "cancelled":
		item.job.Status = StatusCancelled
	}

	item.job.Progress = prog.Progress
	item.job.Speed = prog.Speed
	item.job.ETA = prog.ETA
}

func (jm *JobManager) SetJobCompleted(id string) {
	jm.mu.RLock()
	item, exists := jm.jobs[id]
	jm.mu.RUnlock()

	if !exists {
		return
	}

	item.mu.Lock()
	defer item.mu.Unlock()

	if item.job.Status == StatusCancelled {
		return
	}

	item.job.Status = StatusCompleted
	item.job.Progress = 100.0
	item.job.Speed = 0
	item.job.ETA = 0
}

func (jm *JobManager) SetJobFailed(id string, err error) {
	jm.mu.RLock()
	item, exists := jm.jobs[id]
	jm.mu.RUnlock()

	if !exists {
		return
	}

	item.mu.Lock()
	defer item.mu.Unlock()

	if item.job.Status == StatusCancelled {
		return
	}

	item.job.Status = StatusFailed
	if err != nil {
		item.job.Error = err.Error()
	} else {
		item.job.Error = "unknown error"
	}
}

func (jm *JobManager) CancelJob(id string) bool {
	jm.mu.RLock()
	item, exists := jm.jobs[id]
	jm.mu.RUnlock()

	if !exists {
		return false
	}

	item.mu.Lock()
	defer item.mu.Unlock()

	if item.job.Status == StatusCompleted || item.job.Status == StatusFailed || item.job.Status == StatusCancelled {
		return false
	}

	item.job.Status = StatusCancelled
	item.job.Error = "download cancelled by user"
	if item.cancel != nil {
		item.cancel()
	}
	return true
}

func (jm *JobManager) CancelAll() {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	for _, item := range jm.jobs {
		item.mu.Lock()
		if item.job.Status != StatusCompleted && item.job.Status != StatusFailed && item.job.Status != StatusCancelled {
			item.job.Status = StatusCancelled
			item.job.Error = "server shutdown"
			if item.cancel != nil {
				item.cancel()
			}
		}
		item.mu.Unlock()
	}
}
