package speakingpipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/objectstorage"
	"github.com/almatkai/ielts-after-cigarette-back/internal/speech"
	"github.com/google/uuid"
)

type Queue interface {
	EnqueueTranscription(context.Context, uuid.UUID) error
	EnqueueAssessment(context.Context, uuid.UUID) error
	PopTranscription(context.Context) (uuid.UUID, error)
	PopAssessment(context.Context) (uuid.UUID, error)
}

type Worker struct {
	repository *attempts.PostgresRepository
	provider   attempts.MaterialProvider
	store      objectstorage.Store
	speech     *speech.Client
	evaluator  attempts.SpeakingEvaluator
	queue      Queue
	logger     *slog.Logger
}

func NewWorker(repository *attempts.PostgresRepository, provider attempts.MaterialProvider, store objectstorage.Store, speechClient *speech.Client, evaluator attempts.SpeakingEvaluator, queue Queue, logger *slog.Logger) *Worker {
	return &Worker{repository: repository, provider: provider, store: store, speech: speechClient, evaluator: evaluator, queue: queue, logger: logger}
}

func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	transcriptionEvents := make(chan uuid.UUID, 8)
	assessmentEvents := make(chan uuid.UUID, 8)
	go w.consume(ctx, w.queue.PopTranscription, transcriptionEvents)
	go w.consume(ctx, w.queue.PopAssessment, assessmentEvents)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case id := <-transcriptionEvents:
			w.processTranscription(ctx, id)
		case id := <-assessmentEvents:
			w.processAssessment(ctx, id)
		case <-ticker.C:
			w.recoverPending(ctx)
		}
	}
}

func (w *Worker) consume(ctx context.Context, pop func(context.Context) (uuid.UUID, error), output chan<- uuid.UUID) {
	for ctx.Err() == nil {
		id, err := pop(ctx)
		if err != nil {
			if ctx.Err() == nil {
				w.logger.Warn("speaking queue pop failed", "error", err)
				time.Sleep(time.Second)
			}
			continue
		}
		select {
		case output <- id:
		case <-ctx.Done():
			return
		}
	}
}

func (w *Worker) recoverPending(ctx context.Context) {
	ids, err := w.repository.PendingTranscriptionIDs(ctx, 20)
	if err == nil {
		for _, id := range ids {
			_ = w.queue.EnqueueTranscription(ctx, id)
		}
	}
	ids, err = w.repository.PendingAssessmentIDs(ctx, 20)
	if err == nil {
		for _, id := range ids {
			_ = w.queue.EnqueueAssessment(ctx, id)
		}
	}
}

func (w *Worker) processTranscription(ctx context.Context, recordingID uuid.UUID) {
	work, claimed, err := w.repository.ClaimTranscription(ctx, recordingID)
	if err != nil || !claimed {
		if err != nil {
			w.logger.Error("claim speaking transcription", "recording_id", recordingID, "error", err)
		}
		return
	}
	object, err := w.store.Open(ctx, work.Recording.StorageKey)
	if err != nil {
		w.failTranscription(ctx, work, err)
		return
	}
	result, callErr := w.speech.Transcribe(ctx, work.Recording.OriginalName, work.Recording.MimeType, object)
	closeErr := object.Close()
	if callErr != nil {
		w.failTranscription(ctx, work, callErr)
		return
	}
	if closeErr != nil {
		w.failTranscription(ctx, work, closeErr)
		return
	}
	transcription := attempts.SpeakingTranscription{
		RecordingID: work.Recording.ID, RecordingRevision: work.Recording.Revision,
		Status: "READY", Provider: "faster-whisper", Model: result.Model,
		Language: result.Language, LanguageProbability: result.LanguageProbability,
		Transcript: strings.TrimSpace(result.Text), AudioDurationMS: int64(result.DurationSeconds * 1000),
		SpeechDurationMS: int64(result.DurationAfterVadSeconds * 1000), ProcessingTimeMS: result.ProcessingTimeMS,
		Metrics: Metrics(result.Words, result.DurationSeconds, result.DurationAfterVadSeconds),
	}
	for _, item := range result.Words {
		transcription.Words = append(transcription.Words, attempts.SpeakingWord(item))
	}
	for _, item := range result.Segments {
		transcription.Segments = append(transcription.Segments, attempts.SpeakingSegment(item))
	}
	updated, err := w.repository.CompleteTranscription(ctx, transcription)
	if err != nil {
		w.logger.Error("complete speaking transcription", "recording_id", recordingID, "error", err)
		return
	}
	if updated {
		_ = w.queue.EnqueueAssessment(ctx, work.Recording.AttemptID)
	}
}

func (w *Worker) failTranscription(ctx context.Context, work attempts.TranscriptionWork, err error) {
	w.logger.Error("speaking transcription failed", "recording_id", work.Recording.ID, "error", err)
	_ = w.repository.FailTranscription(ctx, work.Recording.ID, work.Recording.Revision, truncate(err.Error(), 2000))
}

func (w *Worker) processAssessment(ctx context.Context, attemptID uuid.UUID) {
	claimed, err := w.repository.ClaimAssessment(ctx, attemptID)
	if err != nil || !claimed {
		return
	}
	attempt, err := w.repository.Get(ctx, attemptID)
	if err != nil {
		w.failAssessment(ctx, attemptID, err)
		return
	}
	if attempt.Status == attempts.StatusSubmitted {
		_ = w.repository.CompleteAssessment(ctx, attemptID)
		return
	}
	material, err := w.provider.GradingStructure(ctx, attempt.MaterialID, attempt.MaterialVersionID)
	if err != nil {
		w.failAssessment(ctx, attemptID, err)
		return
	}
	answers, err := w.repository.ListAnswers(ctx, attemptID)
	if err != nil {
		w.failAssessment(ctx, attemptID, err)
		return
	}
	byPart := map[uuid.UUID]map[string]any{}
	for _, item := range answers {
		byPart[item.QuestionID] = item.Answer
	}
	recordings, err := w.repository.ListSpeakingRecordings(ctx, attemptID)
	if err != nil {
		w.failAssessment(ctx, attemptID, err)
		return
	}
	recordingByPart := map[uuid.UUID]attempts.SpeakingRecording{}
	for _, item := range recordings {
		recordingByPart[item.PartID] = item
	}
	request := attempts.SpeakingEvaluationRequest{ExamType: material.ExamType}
	gradedAnswers := make([]attempts.Answer, 0, len(material.SpeakingParts))
	for _, part := range material.SpeakingParts {
		transcript, _ := byPart[part.ID]["value"].(string)
		var metrics *attempts.SpeakingMetrics
		if recording, ok := recordingByPart[part.ID]; ok {
			if recording.Transcription == nil || recording.Transcription.Status == "QUEUED" || recording.Transcription.Status == "PROCESSING" {
				_ = w.repository.DeferAssessment(ctx, attemptID)
				return
			}
			if recording.Transcription.Status != "READY" {
				w.failAssessment(ctx, attemptID, errors.New("recording transcription failed"))
				return
			}
			transcript = recording.Transcription.Transcript
			metrics = &recording.Transcription.Metrics
		}
		transcript = strings.TrimSpace(transcript)
		if transcript == "" {
			w.failAssessment(ctx, attemptID, attempts.ErrSpeakingIncomplete)
			return
		}
		request.Parts = append(request.Parts, attempts.SpeakingPartAnswer{Part: part, Transcript: transcript, Metrics: metrics})
		gradedAnswers = append(gradedAnswers, attempts.Answer{QuestionID: part.ID, Answer: map[string]any{"value": transcript}})
	}
	evaluation, err := w.evaluator.EvaluateSpeaking(ctx, request)
	if err != nil {
		w.failAssessment(ctx, attemptID, err)
		return
	}
	evaluation.AttemptID = attemptID
	evaluation.PronunciationAvailable = false
	evaluation.Criteria.Pronunciation = attempts.SpeakingCriterion{}
	band := evaluation.OverallBand
	if err := w.repository.Submit(ctx, attempts.SubmitResult{
		AttemptID: attemptID, UserID: attempt.UserID, Skill: attempts.MaterialSpeaking,
		Answers: gradedAnswers, Score: int(band * 10), MaxScore: 90, Band: band,
		Accuracy: band / 9 * 100, SpeakingEvaluation: &evaluation,
	}); err != nil {
		w.failAssessment(ctx, attemptID, err)
		return
	}
	_ = w.repository.CompleteAssessment(ctx, attemptID)
}

func (w *Worker) failAssessment(ctx context.Context, attemptID uuid.UUID, err error) {
	w.logger.Error("speaking assessment failed", "attempt_id", attemptID, "error", err)
	_ = w.repository.FailAssessment(ctx, attemptID, truncate(err.Error(), 2000))
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return fmt.Sprintf("%s...", value[:limit-3])
}
