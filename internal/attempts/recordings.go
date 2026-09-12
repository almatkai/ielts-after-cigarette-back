package attempts

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

const defaultMaxSpeakingAssessmentAudioBytes int64 = 12 << 20

type speakingRecordingRepository interface {
	UpsertSpeakingRecording(context.Context, SpeakingRecording) (SpeakingRecording, string, error)
	ListSpeakingRecordings(context.Context, uuid.UUID) ([]SpeakingRecording, error)
	GetSpeakingRecording(context.Context, uuid.UUID, uuid.UUID) (SpeakingRecording, error)
}

func (s *Service) StoreSpeakingRecording(
	ctx context.Context,
	userID, attemptID, partID uuid.UUID,
	header *multipart.FileHeader,
	source io.Reader,
) (SpeakingRecording, error) {
	attempt, err := s.own(ctx, userID, attemptID)
	if err != nil {
		return SpeakingRecording{}, err
	}
	if attempt.Status != StatusInProgress {
		return SpeakingRecording{}, ErrAlreadySubmitted
	}
	if attempt.MaterialType != MaterialSpeaking {
		return SpeakingRecording{}, ErrUnsupportedMaterial
	}
	provider, err := s.provider(MaterialSpeaking)
	if err != nil {
		return SpeakingRecording{}, err
	}
	material, err := provider.GradingStructure(ctx, attempt.MaterialID, attempt.MaterialVersionID)
	if err != nil {
		return SpeakingRecording{}, err
	}
	validPart := false
	for _, part := range material.SpeakingParts {
		if part.ID == partID {
			validPart = true
			break
		}
	}
	if !validPart {
		return SpeakingRecording{}, ErrNotFound
	}
	if header == nil || header.Size < 1 {
		return SpeakingRecording{}, fmt.Errorf("recording file is required")
	}
	limit := s.maxSpeakingMedia
	if limit < 1 {
		limit = defaultMaxSpeakingAssessmentAudioBytes
	}
	if header.Size > limit {
		return SpeakingRecording{}, ErrRecordingTooLarge
	}
	ext, mimeType, ok := audioExtension(header.Filename, header.Header.Get("Content-Type"))
	if !ok {
		return SpeakingRecording{}, fmt.Errorf("unsupported audio file type")
	}
	if s.speakingMediaDir == "" {
		return SpeakingRecording{}, fmt.Errorf("speaking recording storage is not configured")
	}
	if err := os.MkdirAll(s.speakingMediaDir, 0o750); err != nil {
		return SpeakingRecording{}, fmt.Errorf("create speaking media directory: %w", err)
	}
	key := uuid.NewString() + ext
	target := filepath.Join(s.speakingMediaDir, key)
	temporary := target + ".upload"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return SpeakingRecording{}, fmt.Errorf("create recording: %w", err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(source, limit+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written > limit {
		_ = os.Remove(temporary)
		if copyErr != nil {
			return SpeakingRecording{}, fmt.Errorf("save recording: %w", copyErr)
		}
		if closeErr != nil {
			return SpeakingRecording{}, fmt.Errorf("close recording: %w", closeErr)
		}
		return SpeakingRecording{}, ErrRecordingTooLarge
	}
	if err = os.Rename(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return SpeakingRecording{}, fmt.Errorf("finalize recording: %w", err)
	}
	repository, ok := s.repository.(speakingRecordingRepository)
	if !ok {
		_ = os.Remove(target)
		return SpeakingRecording{}, ErrNotFound
	}
	recording, previousKey, err := repository.UpsertSpeakingRecording(ctx, SpeakingRecording{
		ID: uuid.New(), AttemptID: attemptID, PartID: partID,
		OriginalName: filepath.Base(header.Filename), MimeType: mimeType,
		StorageKey: key, ByteSize: written,
	})
	if err != nil {
		_ = os.Remove(target)
		return SpeakingRecording{}, err
	}
	if previousKey != "" && previousKey != key && filepath.Base(previousKey) == previousKey {
		_ = os.Remove(filepath.Join(s.speakingMediaDir, previousKey))
	}
	return recording, nil
}

func (s *Service) SpeakingRecording(ctx context.Context, userID, attemptID, partID uuid.UUID) (SpeakingRecording, string, error) {
	attempt, err := s.own(ctx, userID, attemptID)
	if err != nil {
		return SpeakingRecording{}, "", err
	}
	if attempt.MaterialType != MaterialSpeaking {
		return SpeakingRecording{}, "", ErrNotFound
	}
	repository, ok := s.repository.(speakingRecordingRepository)
	if !ok {
		return SpeakingRecording{}, "", ErrNotFound
	}
	recording, err := repository.GetSpeakingRecording(ctx, attemptID, partID)
	if err != nil {
		return SpeakingRecording{}, "", err
	}
	if s.speakingMediaDir == "" || filepath.Base(recording.StorageKey) != recording.StorageKey {
		return SpeakingRecording{}, "", ErrRecordingNotFound
	}
	return recording, filepath.Join(s.speakingMediaDir, recording.StorageKey), nil
}

func (s *Service) speakingRecordings(ctx context.Context, attemptID uuid.UUID) ([]SpeakingRecording, error) {
	repository, ok := s.repository.(speakingRecordingRepository)
	if !ok {
		return nil, ErrNotFound
	}
	items, err := repository.ListSpeakingRecordings(ctx, attemptID)
	if items == nil {
		items = []SpeakingRecording{}
	}
	return items, err
}

func (s *Service) readSpeakingAudio(recording SpeakingRecording) (*SpeakingAudio, error) {
	limit := s.maxSpeakingMedia
	if limit < 1 {
		limit = defaultMaxSpeakingAssessmentAudioBytes
	}
	if recording.ByteSize < 1 || recording.ByteSize > limit || s.speakingMediaDir == "" || filepath.Base(recording.StorageKey) != recording.StorageKey {
		return nil, ErrRecordingTooLarge
	}
	file, err := os.Open(filepath.Join(s.speakingMediaDir, recording.StorageKey))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrRecordingNotFound
		}
		return nil, fmt.Errorf("open speaking recording: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read speaking recording: %w", err)
	}
	if len(data) == 0 || int64(len(data)) > limit {
		return nil, ErrRecordingTooLarge
	}
	return &SpeakingAudio{Data: data, Format: audioFormat(recording.MimeType, recording.StorageKey)}, nil
}

func audioExtension(filename, contentType string) (string, string, bool) {
	ext := strings.ToLower(filepath.Ext(filename))
	if ext == ".mp4" {
		ext = ".m4a"
	}
	mimeTypes := map[string]string{
		".webm": "audio/webm", ".ogg": "audio/ogg", ".wav": "audio/wav",
		".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".aac": "audio/aac",
	}
	mimeType, ok := mimeTypes[ext]
	if !ok {
		return "", "", false
	}
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if contentType != "" && contentType != "application/octet-stream" && !strings.HasPrefix(contentType, "audio/") {
		return "", "", false
	}
	return ext, mimeType, true
}

func audioFormat(mimeType, storageKey string) string {
	switch strings.ToLower(mimeType) {
	case "audio/webm":
		return "webm"
	case "audio/ogg":
		return "ogg"
	case "audio/wav", "audio/x-wav":
		return "wav"
	case "audio/mpeg":
		return "mp3"
	case "audio/mp4", "audio/x-m4a":
		return "m4a"
	case "audio/aac":
		return "aac"
	}
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(storageKey)), ".")
}
