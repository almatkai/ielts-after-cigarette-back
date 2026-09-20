package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
)

type Word struct {
	Word        string  `json:"word"`
	Start       float64 `json:"start"`
	End         float64 `json:"end"`
	Probability float64 `json:"probability"`
}

type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type Result struct {
	Text                    string    `json:"text"`
	Language                string    `json:"language"`
	LanguageProbability     float64   `json:"languageProbability"`
	DurationSeconds         float64   `json:"durationSeconds"`
	DurationAfterVadSeconds float64   `json:"durationAfterVadSeconds"`
	ProcessingTimeMS        int64     `json:"processingTimeMs"`
	Model                   string    `json:"model"`
	Words                   []Word    `json:"words"`
	Segments                []Segment `json:"segments"`
}

type Client struct {
	endpoint string
	token    string
	http     *http.Client
}

func NewClient(endpoint, token string, httpClient *http.Client) *Client {
	return &Client{
		endpoint: strings.TrimRight(strings.TrimSpace(endpoint), "/"),
		token:    strings.TrimSpace(token),
		http:     httpClient,
	}
}

func (c *Client) Transcribe(ctx context.Context, name, contentType string, source io.Reader) (Result, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("audio", filepath.Base(name))
	if err != nil {
		return Result{}, fmt.Errorf("create speech multipart: %w", err)
	}
	if _, err := io.Copy(part, source); err != nil {
		return Result{}, fmt.Errorf("copy speech audio: %w", err)
	}
	if err := writer.Close(); err != nil {
		return Result{}, fmt.Errorf("close speech multipart: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/transcribe", &body)
	if err != nil {
		return Result{}, fmt.Errorf("create speech request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if contentType != "" {
		req.Header.Set("X-Audio-Content-Type", contentType)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.http.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("call speech service: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return Result{}, fmt.Errorf("read speech response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Result{}, fmt.Errorf("speech service status %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	var result Result
	if err := json.Unmarshal(data, &result); err != nil {
		return Result{}, fmt.Errorf("decode speech response: %w", err)
	}
	if strings.TrimSpace(result.Text) == "" || result.DurationSeconds <= 0 {
		return Result{}, fmt.Errorf("speech service returned an empty transcription")
	}
	return result, nil
}
