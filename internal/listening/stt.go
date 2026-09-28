package listening

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type STTSegment struct {
	ID    int     `json:"id"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type STTResult struct {
	Text     string       `json:"text"`
	Duration float64      `json:"duration"`
	Segments []STTSegment `json:"segments"`
}

type QuestionAlignment struct {
	Number int     `json:"number"`
	Start  float64 `json:"start"`
	End    float64 `json:"end"`
	Quote  string  `json:"quote"`
	Hint   string  `json:"hint"`
}

type TranscribeResult struct {
	Test       Test                `json:"test"`
	Transcript string              `json:"transcript"`
	Segments   []STTSegment        `json:"segments"`
	Alignments []QuestionAlignment `json:"alignments"`
}

type STTService struct {
	sttURL     string
	sttKey     string
	aiURL      string
	aiKey      string
	aiModel    string
	httpClient *http.Client
}

func NewSTTService(sttURL, sttKey, aiURL, aiKey, aiModel string) *STTService {
	return &STTService{
		sttURL:     strings.TrimSpace(sttURL),
		sttKey:     strings.TrimSpace(sttKey),
		aiURL:      strings.TrimSpace(aiURL),
		aiKey:      strings.TrimSpace(aiKey),
		aiModel:    strings.TrimSpace(aiModel),
		httpClient: &http.Client{Timeout: 5 * time.Minute},
	}
}

// convertToWav transcodes a compressed recording into 16 kHz mono PCM WAV so
// the STT endpoint receives a format it reliably understands. The source
// reader is fully consumed by ffmpeg, so a failure here must abort instead of
// falling through: otherwise the caller would upload the already-drained
// reader and send an empty payload to the STT API.
func convertToWav(ctx context.Context, filename string, audioStream io.Reader) (io.Reader, string, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, "", fmt.Errorf("ffmpeg is required to transcode %s but was not found in PATH: %w", filename, err)
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", "pipe:0", "-f", "wav", "-ar", "16000", "-ac", "1", "pipe:1")
	cmd.Stdin = audioStream
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		stderr := strings.TrimSpace(errBuf.String())
		if stderr == "" {
			stderr = "<no stderr output>"
		}
		slog.ErrorContext(ctx, "ffmpeg conversion failed", "filename", filename, "error", err, "stderr", stderr)
		return nil, "", fmt.Errorf("ffmpeg conversion failed for %s: %w (stderr: %s)", filename, err, stderr)
	}
	if out.Len() == 0 {
		slog.ErrorContext(ctx, "ffmpeg produced empty wav output", "filename", filename, "stderr", strings.TrimSpace(errBuf.String()))
		return nil, "", fmt.Errorf("ffmpeg conversion produced empty output for %s", filename)
	}

	ext := strings.ToLower(filepath.Ext(filename))
	return &out, strings.TrimSuffix(filename, ext) + ".wav", nil
}

func (s *STTService) Transcribe(ctx context.Context, filename string, audioStream io.Reader) (STTResult, error) {
	if s.sttURL == "" {
		return STTResult{}, errors.New("STT_API_URL is not configured")
	}

	ext := strings.ToLower(filepath.Ext(filename))
	if ext == ".webm" || ext == ".ogg" || ext == ".m4a" {
		converted, convertedName, err := convertToWav(ctx, filename, audioStream)
		if err != nil {
			return STTResult{}, err
		}
		audioStream = converted
		filename = convertedName
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return STTResult{}, fmt.Errorf("create multipart file: %w", err)
	}
	if _, err := io.Copy(part, audioStream); err != nil {
		return STTResult{}, fmt.Errorf("copy audio stream: %w", err)
	}

	_ = writer.WriteField("model", "speech-to-text")
	_ = writer.WriteField("language", "en")
	_ = writer.WriteField("response_format", "verbose_json")

	if err := writer.Close(); err != nil {
		return STTResult{}, fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.sttURL, &body)
	if err != nil {
		return STTResult{}, fmt.Errorf("create stt request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	if s.sttKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.sttKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return STTResult{}, fmt.Errorf("call stt endpoint: %w", err)
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return STTResult{}, fmt.Errorf("read stt response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return STTResult{}, fmt.Errorf("stt error %d: %s", resp.StatusCode, string(respData))
	}

	type rawSegment struct {
		ID    int     `json:"id"`
		Start float64 `json:"start"`
		End   float64 `json:"end"`
		Text  string  `json:"text"`
	}

	type rawSTTResponse struct {
		Text     string       `json:"text"`
		Duration any          `json:"duration"`
		Segments []rawSegment `json:"segments"`
	}

	var raw rawSTTResponse
	if err := json.Unmarshal(respData, &raw); err != nil {
		return STTResult{}, fmt.Errorf("decode stt response: %w", err)
	}

	var dur float64
	switch v := raw.Duration.(type) {
	case float64:
		dur = v
	case string:
		dur, _ = strconv.ParseFloat(v, 64)
	}

	result := STTResult{
		Text:     strings.TrimSpace(raw.Text),
		Duration: dur,
		Segments: make([]STTSegment, len(raw.Segments)),
	}
	for i, seg := range raw.Segments {
		result.Segments[i] = STTSegment{
			ID:    seg.ID,
			Start: seg.Start,
			End:   seg.End,
			Text:  strings.TrimSpace(seg.Text),
		}
	}
	return result, nil
}

func (s *STTService) Align(ctx context.Context, segments []STTSegment, questions []Question) ([]QuestionAlignment, error) {
	if len(segments) == 0 || len(questions) == 0 {
		return nil, nil
	}
	if s.aiURL == "" {
		return nil, errors.New("AI endpoint is not configured")
	}

	var transcriptBuilder strings.Builder
	for _, seg := range segments {
		transcriptBuilder.WriteString(fmt.Sprintf("[%.1fs - %.1fs] %s\n", seg.Start, seg.End, seg.Text))
	}

	var questionsBuilder strings.Builder
	for _, q := range questions {
		ansStr, _ := json.Marshal(q.Answer)
		questionsBuilder.WriteString(fmt.Sprintf("Question %d: %s | Answer: %s\n", q.Number, q.Prompt, string(ansStr)))
	}

	prompt := fmt.Sprintf(`You are an expert IELTS Listening analyst.
Given an IELTS listening audio transcript broken down into timestamped segments (start and end in seconds) and a list of questions with their answers, identify the exact audio timestamp segment where the answer to each question is spoken.

For each question:
1. "number": integer question number
2. "start": timestamp in seconds (float) where the speaker begins answering this question
3. "end": timestamp in seconds (float) where the speaker finishes answering this question
4. "quote": exact sentence/quote spoken by the speaker in the transcript that proves the answer
5. "hint": a helpful guidance hint in Russian directing the student to listen for specific cues or keywords without directly giving away the solution.

Transcript segments:
%s

Questions:
%s

Respond ONLY with valid JSON in this exact structure:
{
  "alignments": [
    {
      "number": 1,
      "start": 14.5,
      "end": 21.0,
      "quote": "The earliest departure date is the 24th of May.",
      "hint": "Слушайте реплику менеджера, когда он называет первую доступную дату вылета."
    }
  ]
}`, transcriptBuilder.String(), questionsBuilder.String())

	type chatMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type chatRequest struct {
		Model       string        `json:"model"`
		Messages    []chatMessage `json:"messages"`
		Temperature float64       `json:"temperature"`
	}

	reqBody, _ := json.Marshal(chatRequest{
		Model: s.aiModel,
		Messages: []chatMessage{
			{Role: "system", Content: "You are an expert IELTS Listening analyst. Map questions to transcript timestamps. Output JSON only."},
			{Role: "user", Content: prompt},
		},
		Temperature: 0.1,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.aiURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create ai alignment request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.aiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.aiKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call ai alignment endpoint: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read ai alignment response: %w", err)
	}

	type chatChoice struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	type chatResponse struct {
		Choices []chatChoice `json:"choices"`
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBytes, &chatResp); err != nil || len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("decode ai response (%s): %w", string(respBytes), err)
	}

	content := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		if len(lines) >= 2 {
			lines = lines[1:]
			if strings.HasPrefix(lines[len(lines)-1], "```") {
				lines = lines[:len(lines)-1]
			}
			content = strings.Join(lines, "\n")
		}
	}

	type alignmentEnvelope struct {
		Alignments []QuestionAlignment `json:"alignments"`
	}
	var env alignmentEnvelope
	if err := json.Unmarshal([]byte(content), &env); err != nil {
		return nil, fmt.Errorf("parse alignment json (%s): %w", content, err)
	}

	return env.Alignments, nil
}
