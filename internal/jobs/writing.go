package jobs

import (
	"context"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	writingAssessmentQueue = "jobs:writing:assessment"
)

type WritingQueue struct{ client *redis.Client }

func NewWritingQueue(client *redis.Client) *WritingQueue { return &WritingQueue{client: client} }

func (q *WritingQueue) EnqueueWritingAssessment(ctx context.Context, attemptID uuid.UUID) error {
	if q.client == nil {
		return nil
	}
	return q.client.LPush(ctx, writingAssessmentQueue, attemptID.String()).Err()
}

func (q *WritingQueue) PopWritingAssessment(ctx context.Context) (uuid.UUID, error) {
	values, err := q.client.BRPop(ctx, 0, writingAssessmentQueue).Result()
	if err != nil {
		return uuid.Nil, err
	}
	return uuid.Parse(values[1])
}
