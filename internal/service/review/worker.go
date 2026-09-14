package review

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"third-party-review/internal/domain"
)

const (
	// pollInterval is how often an idle worker looks for queued work. A short
	// interval is cheap against a local Postgres and keeps the UI responsive
	// between enqueue and the first progress update.
	pollInterval = 2 * time.Second
	// heartbeatInterval is how often a running job proves its worker is alive.
	heartbeatInterval = 15 * time.Second
	// stalledAfter is how long without a heartbeat before a job is assumed
	// orphaned by a restart and re-queued.
	stalledAfter = 2 * time.Minute
	// jobTimeout caps a single run so one pathological assessment cannot hold
	// a worker forever.
	jobTimeout = 45 * time.Minute
)

// Worker drains the review job queue. Concurrency is a small fixed number of
// goroutines rather than a job broker: this is one internal team's workload,
// and the queue table plus SKIP LOCKED already gives at-most-once claiming.
type Worker struct {
	svc  *Service
	jobs domain.JobRepository
	log  *slog.Logger
	n    int

	wg sync.WaitGroup
}

// NewWorker constructs the background worker. n is the number of concurrent
// review runs.
func NewWorker(svc *Service, jobs domain.JobRepository, n int, log *slog.Logger) *Worker {
	if n < 1 {
		n = 1
	}
	return &Worker{svc: svc, jobs: jobs, log: log, n: n}
}

// Start launches the worker goroutines. They stop when ctx is cancelled.
func (w *Worker) Start(ctx context.Context) {
	// A restart leaves jobs marked running with no worker behind them. Reclaim
	// them first, or their assessments sit in `reviewing` with a progress bar
	// that never moves.
	if n, err := w.jobs.ReclaimStalled(ctx, stalledAfter); err != nil {
		w.log.Error("could not reclaim stalled review jobs", "error", err)
	} else if n > 0 {
		w.log.Warn("re-queued review jobs orphaned by a restart", "count", n)
	}

	for i := 0; i < w.n; i++ {
		w.wg.Add(1)
		go func(id int) {
			defer w.wg.Done()
			w.loop(ctx, id)
		}(i)
	}
	w.log.Info("review worker started", "concurrency", w.n)
}

// Wait blocks until every worker goroutine has returned.
func (w *Worker) Wait() { w.wg.Wait() }

func (w *Worker) loop(ctx context.Context, id int) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	reclaim := time.NewTicker(stalledAfter)
	defer reclaim.Stop()

	for {
		select {
		case <-ctx.Done():
			w.log.Info("review worker stopping", "worker", id)
			return
		case <-reclaim.C:
			if n, err := w.jobs.ReclaimStalled(ctx, stalledAfter); err == nil && n > 0 {
				w.log.Warn("re-queued stalled review jobs", "count", n)
			}
		case <-ticker.C:
			for {
				claimed, err := w.claimAndRun(ctx)
				if err != nil || !claimed {
					break
				}
				// Drain the queue rather than waiting a full tick per job.
			}
		}
	}
}

// claimAndRun takes one job if available and runs it to completion. It returns
// whether a job was claimed.
func (w *Worker) claimAndRun(ctx context.Context) (bool, error) {
	job, err := w.jobs.ClaimNext(ctx)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		if ctx.Err() == nil {
			w.log.Error("could not claim a review job", "error", err)
		}
		return false, err
	}

	w.runJob(ctx, job)
	return true, nil
}

// runJob executes one job with a heartbeat and a timeout, and records the
// outcome. A panic inside a review is contained here: it fails that job rather
// than taking the process down mid-assessment.
func (w *Worker) runJob(ctx context.Context, job *domain.ReviewJob) {
	runCtx, cancel := context.WithTimeout(ctx, jobTimeout)
	defer cancel()

	stop := w.startHeartbeat(runCtx, job.ID)
	defer stop()

	var runErr error
	func() {
		defer func() {
			if p := recover(); p != nil {
				runErr = errors.New("the review crashed unexpectedly; see the server log for details")
				w.log.Error("panic during review", "job_id", job.ID, "assessment_id", job.AssessmentID, "panic", p)
			}
		}()
		runErr = w.svc.Run(runCtx, job)
	}()

	// Recording the outcome must not use the run context: if the run was
	// cancelled or timed out, a cancelled context would also prevent us
	// writing down why.
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer finishCancel()

	if runErr != nil {
		w.log.Error("review job failed", "job_id", job.ID, "assessment_id", job.AssessmentID, "error", runErr)
	}
	if err := w.svc.Finish(finishCtx, job, runErr); err != nil {
		w.log.Error("could not record the job outcome", "job_id", job.ID, "error", err)
	}
}

// startHeartbeat keeps the job's heartbeat fresh while it runs, so the stalled
// reclaimer can tell a long review from a dead worker.
func (w *Worker) startHeartbeat(ctx context.Context, jobID int64) func() {
	done := make(chan struct{})
	var once sync.Once

	go func() {
		t := time.NewTicker(heartbeatInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-t.C:
				if err := w.jobs.Heartbeat(ctx, jobID); err != nil && ctx.Err() == nil {
					w.log.Warn("heartbeat failed", "job_id", jobID, "error", err)
				}
			}
		}
	}()

	return func() { once.Do(func() { close(done) }) }
}
