package store

import (
	"context"
	"errors"
	"testing"
)

func newJob(chatID int64, messageID int, unique string) Job {
	return Job{ChatID: chatID, MessageID: messageID, FileUniqueID: unique, FileID: "file-" + unique,
		MimeType: "image/jpeg", Origin: JobOriginMessage}
}

func TestEnqueueJobIsIdempotentPerDocument(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	first, created, err := s.EnqueueJob(ctx, newJob(chatA, 5, "u1"))
	if err != nil || !created {
		t.Fatalf("primera entrega: created=%v err=%v", created, err)
	}
	again, created, err := s.EnqueueJob(ctx, newJob(chatA, 5, "u1"))
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("entrega repetida: %+v created=%v err=%v", again, created, err)
	}
	if _, created, _ := s.EnqueueJob(ctx, newJob(chatA, 5, "u2")); !created {
		t.Error("otro archivo del mismo mensaje debe ser otro trabajo")
	}
	if _, created, _ := s.EnqueueJob(ctx, newJob(chatB, 5, "u1")); !created {
		t.Error("el mismo mensaje en otro chat debe ser otro trabajo")
	}
	jobs, err := s.Jobs(ctx, chatA, 10)
	if err != nil || len(jobs) != 2 || jobs[0].State != JobQueued || jobs[0].Kind != JobKindPhoto {
		t.Fatalf("trabajos = %+v, %v", jobs, err)
	}
}

func TestJobKindFollowsMimeType(t *testing.T) {
	s := openTestStore(t)
	pdf := newJob(chatA, 1, "pdf")
	pdf.MimeType = "application/pdf"
	job, _, err := s.EnqueueJob(context.Background(), pdf)
	if err != nil || job.Kind != JobKindPDF {
		t.Fatalf("kind = %q, %v", job.Kind, err)
	}
}

func TestAdvanceJobOnlyFollowsTheExpectedState(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	job, _, _ := s.EnqueueJob(ctx, newJob(chatA, 1, "u"))

	if err := s.AdvanceJob(ctx, chatA, job.ID, JobReading, JobDone); !errors.Is(err, ErrJobGone) {
		t.Errorf("salto de estado = %v, se esperaba ErrJobGone", err)
	}
	if err := s.AdvanceJob(ctx, chatB, job.ID, JobQueued, JobDownloading); !errors.Is(err, ErrJobGone) {
		t.Errorf("otro chat movió el trabajo: %v", err)
	}
	for _, step := range [][2]JobState{{JobQueued, JobDownloading}, {JobDownloading, JobReading}, {JobReading, JobFailedRead}} {
		if err := s.AdvanceJob(ctx, chatA, job.ID, step[0], step[1]); err != nil {
			t.Fatalf("%s -> %s: %v", step[0], step[1], err)
		}
	}
}

func TestRecoverJobsRequeuesAndMarksReadingAsUncertain(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	queued, _, _ := s.EnqueueJob(ctx, newJob(chatA, 1, "q"))
	downloading, _, _ := s.EnqueueJob(ctx, newJob(chatA, 2, "d"))
	reading, _, _ := s.EnqueueJob(ctx, newJob(chatB, 3, "r"))
	failed, _, _ := s.EnqueueJob(ctx, newJob(chatB, 4, "f"))
	mustAdvance(t, s, downloading, JobQueued, JobDownloading)
	mustAdvance(t, s, reading, JobQueued, JobDownloading)
	mustAdvance(t, s, reading, JobDownloading, JobReading)
	mustAdvance(t, s, failed, JobQueued, JobDownloading)
	mustAdvance(t, s, failed, JobDownloading, JobFailedDownload)

	requeued, uncertain, err := s.RecoverJobs(ctx)

	if err != nil || len(requeued) != 2 || requeued[0].ID != queued.ID || requeued[1].ID != downloading.ID {
		t.Fatalf("reencolados = %+v, %v", requeued, err)
	}
	if len(uncertain) != 1 || uncertain[0].ID != reading.ID || uncertain[0].State != JobUncertain {
		t.Fatalf("inciertos = %+v", uncertain)
	}
	again, uncertainAgain, _ := s.RecoverJobs(ctx)
	if len(uncertainAgain) != 0 || len(again) != 2 {
		t.Errorf("segunda recuperación: %d reencolados, %d inciertos", len(again), len(uncertainAgain))
	}
}

func TestRetryJobOnlyForFailedOrUncertain(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	job, _, _ := s.EnqueueJob(ctx, newJob(chatA, 1, "u"))
	if _, err := s.RetryJob(ctx, chatA, job.ID); !errors.Is(err, ErrJobGone) {
		t.Errorf("reintento de un trabajo en cola = %v", err)
	}
	mustAdvance(t, s, job, JobQueued, JobDownloading)
	mustAdvance(t, s, job, JobDownloading, JobFailedDownload)
	if _, err := s.RetryJob(ctx, chatB, job.ID); !errors.Is(err, ErrJobGone) {
		t.Errorf("otro chat reintentó: %v", err)
	}
	retried, err := s.RetryJob(ctx, chatA, job.ID)
	if err != nil || retried.State != JobQueued || retried.FileID != job.FileID {
		t.Fatalf("reintento = %+v, %v", retried, err)
	}
}

func TestDraftLinkedToJobMarksItDoneAtomically(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	job, _, _ := s.EnqueueJob(ctx, newJob(chatA, 1, "u"))
	mustAdvance(t, s, job, JobQueued, JobDownloading)
	mustAdvance(t, s, job, JobDownloading, JobReading)
	d := newDraft(sampleInvoice("001-001-0000001", 110000))
	d.JobID = job.ID

	id, err := s.CreateDraft(ctx, chatA, d)

	if err != nil {
		t.Fatal(err)
	}
	jobs, _ := s.Jobs(ctx, chatA, 10)
	if jobs[0].State != JobDone || jobs[0].InvoiceID != id {
		t.Errorf("trabajo = %+v, borrador %d", jobs[0], id)
	}
}

func TestDraftForDeletedJobIsRefused(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	job, _, _ := s.EnqueueJob(ctx, newJob(chatA, 1, "u"))
	mustAdvance(t, s, job, JobQueued, JobDownloading)
	mustAdvance(t, s, job, JobDownloading, JobReading)
	if _, err := s.DeleteChat(ctx, chatA); err != nil {
		t.Fatal(err)
	}
	d := newDraft(sampleInvoice("001-001-0000001", 110000))
	d.JobID = job.ID

	if _, err := s.CreateDraft(ctx, chatA, d); !errors.Is(err, ErrJobGone) {
		t.Fatalf("CreateDraft = %v, se esperaba ErrJobGone", err)
	}
	if drafts, _ := s.Drafts(ctx, chatA); len(drafts) != 0 {
		t.Errorf("quedó un borrador tras el borrado: %d", len(drafts))
	}
}

func TestDeleteChatRemovesOnlyItsJobs(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	_, _, _ = s.EnqueueJob(ctx, newJob(chatA, 1, "a"))
	_, _, _ = s.EnqueueJob(ctx, newJob(chatB, 1, "b"))

	if _, err := s.DeleteChat(ctx, chatA); err != nil {
		t.Fatal(err)
	}
	if jobs, _ := s.Jobs(ctx, chatA, 10); len(jobs) != 0 {
		t.Errorf("trabajos de A: %d", len(jobs))
	}
	if jobs, _ := s.Jobs(ctx, chatB, 10); len(jobs) != 1 {
		t.Errorf("trabajos de B: %d", len(jobs))
	}
}

func TestPruneJobsDropsOnlyOldFinishedOnes(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	old, _, _ := s.EnqueueJob(ctx, newJob(chatA, 1, "old"))
	active, _, _ := s.EnqueueJob(ctx, newJob(chatA, 2, "active"))
	mustAdvance(t, s, old, JobQueued, JobDownloading)
	mustAdvance(t, s, old, JobDownloading, JobFailedDownload)
	_, _ = s.db.Exec(`UPDATE jobs SET updated_at = '2020-01-01T00:00:00Z'`)

	if err := s.PruneJobs(ctx, JobRetention); err != nil {
		t.Fatal(err)
	}
	jobs, _ := s.Jobs(ctx, chatA, 10)
	if len(jobs) != 1 || jobs[0].ID != active.ID {
		t.Fatalf("trabajos tras la limpieza = %+v", jobs)
	}
}

func mustAdvance(t *testing.T, s *Store, job Job, from, to JobState) {
	t.Helper()
	if err := s.AdvanceJob(context.Background(), job.ChatID, job.ID, from, to); err != nil {
		t.Fatalf("%s -> %s: %v", from, to, err)
	}
}
