package jobs

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	transcriptionQueue = "jobs:speaking:transcription"
	assessmentQueue    = "jobs:speaking:assessment"
)

type SpeakingQueue struct{ client *redis.Client }

func NewSpeakingQueue(client *redis.Client) *SpeakingQueue { return &SpeakingQueue{client: client} }

func (q *SpeakingQueue) EnqueueTranscription(ctx context.Context, recordingID uuid.UUID) error {
	if q.client == nil {
		return nil
	}
	return q.client.LPush(ctx, transcriptionQueue, recordingID.String()).Err()
}

func (q *SpeakingQueue) EnqueueAssessment(ctx context.Context, attemptID uuid.UUID) error {
	if q.client == nil {
		return nil
	}
	return q.client.LPush(ctx, assessmentQueue, attemptID.String()).Err()
}

func (q *SpeakingQueue) PopTranscription(ctx context.Context) (uuid.UUID, error) {
	return q.pop(ctx, transcriptionQueue)
}

func (q *SpeakingQueue) PopAssessment(ctx context.Context) (uuid.UUID, error) {
	return q.pop(ctx, assessmentQueue)
}

func (q *SpeakingQueue) pop(ctx context.Context, key string) (uuid.UUID, error) {
	if q.client == nil {
		<-ctx.Done()
		return uuid.Nil, ctx.Err()
	}
	values, err := q.client.BRPop(ctx, 1*time.Second, key).Result()
	if err != nil {
		return uuid.Nil, err
	}
	return uuid.Parse(values[1])
}
