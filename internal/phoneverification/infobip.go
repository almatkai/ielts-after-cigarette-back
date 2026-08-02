package phoneverification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

type InfobipSender struct {
	baseURL    string
	apiKey     string
	sender     string
	template   string
	language   string
	httpClient *http.Client
}

func NewInfobipSender(
	baseURL string,
	apiKey string,
	sender string,
	template string,
	language string,
	httpClient *http.Client,
) *InfobipSender {
	return &InfobipSender{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:     strings.TrimSpace(apiKey),
		sender:     strings.TrimSpace(sender),
		template:   strings.TrimSpace(template),
		language:   strings.TrimSpace(language),
		httpClient: httpClient,
	}
}

func (s *InfobipSender) Configured() bool {
	return s.apiKey != "" && s.sender != "" && s.template != ""
}

func (s *InfobipSender) SendCode(ctx context.Context, phone, code string) error {
	if !s.Configured() {
		return ErrSenderNotConfigured
	}
	payload := infobipTemplateRequest{Messages: []infobipTemplateMessage{{
		From:      s.sender,
		To:        strings.TrimPrefix(phone, "+"),
		MessageID: uuid.NewString(),
		Content: infobipTemplateContent{
			TemplateName: s.template,
			TemplateData: infobipTemplateData{
				Body:    infobipTemplateBody{Placeholders: []string{code}},
				Buttons: []infobipTemplateButton{{Type: "URL", Parameter: code}},
			},
			Language: s.language,
		},
	}}}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode Infobip request: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		s.baseURL+"/whatsapp/1/message/template",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("create Infobip request: %w", err)
	}
	request.Header.Set("Authorization", "App "+s.apiKey)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")

	response, err := s.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("call Infobip API: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("infobip API returned HTTP %d", response.StatusCode)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	return nil
}

type infobipTemplateRequest struct {
	Messages []infobipTemplateMessage `json:"messages"`
}

type infobipTemplateMessage struct {
	From      string                 `json:"from"`
	To        string                 `json:"to"`
	MessageID string                 `json:"messageId"`
	Content   infobipTemplateContent `json:"content"`
}

type infobipTemplateContent struct {
	TemplateName string              `json:"templateName"`
	TemplateData infobipTemplateData `json:"templateData"`
	Language     string              `json:"language"`
}

type infobipTemplateData struct {
	Body    infobipTemplateBody     `json:"body"`
	Buttons []infobipTemplateButton `json:"buttons"`
}

type infobipTemplateBody struct {
	Placeholders []string `json:"placeholders"`
}

type infobipTemplateButton struct {
	Type      string `json:"type"`
	Parameter string `json:"parameter"`
}
