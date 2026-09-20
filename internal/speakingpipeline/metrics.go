package speakingpipeline

import (
	"math"
	"strings"
	"unicode"

	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/speech"
)

const longPauseSeconds = 1.5

func Metrics(words []speech.Word, recordingDuration, speechDuration float64) attempts.SpeakingMetrics {
	result := attempts.SpeakingMetrics{
		RecordingDurationSeconds: round(recordingDuration),
		SpeechDurationSeconds:    round(speechDuration),
		WordCount:                len(words),
		Fillers:                  map[string]int{},
	}
	if recordingDuration > 0 {
		result.SpeechRateWPM = round(float64(len(words)) / recordingDuration * 60)
	}
	if speechDuration > 0 {
		result.ArticulationRateWPM = round(float64(len(words)) / speechDuration * 60)
	}
	for i := 1; i < len(words); i++ {
		pause := words[i].Start - words[i-1].End
		if pause >= longPauseSeconds {
			result.LongPauseCount++
			result.TotalLongPauseSeconds += pause
			result.MaxPauseSeconds = math.Max(result.MaxPauseSeconds, pause)
		}
	}
	if result.LongPauseCount > 0 {
		result.AverageLongPauseSeconds = result.TotalLongPauseSeconds / float64(result.LongPauseCount)
	}
	result.TotalLongPauseSeconds = round(result.TotalLongPauseSeconds)
	result.AverageLongPauseSeconds = round(result.AverageLongPauseSeconds)
	result.MaxPauseSeconds = round(result.MaxPauseSeconds)

	tokens := make([]string, 0, len(words))
	for _, item := range words {
		tokens = append(tokens, normalizeWord(item.Word))
	}
	for i, token := range tokens {
		if token == "um" || token == "uh" || token == "erm" || token == "er" {
			result.Fillers[token]++
			result.FillerCount++
		}
		if token == "you" && i+1 < len(tokens) && tokens[i+1] == "know" {
			result.Fillers["you know"]++
			result.FillerCount++
		}
	}
	return result
}

func normalizeWord(value string) string {
	return strings.TrimFunc(strings.ToLower(strings.TrimSpace(value)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func round(value float64) float64 { return math.Round(value*100) / 100 }
