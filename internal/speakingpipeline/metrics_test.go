package speakingpipeline

import (
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/speech"
)

func TestMetricsUsesFullAndSpeechDuration(t *testing.T) {
	words := []speech.Word{
		{Word: "Um", Start: 1, End: 1.2},
		{Word: "you", Start: 3, End: 3.2},
		{Word: "know", Start: 3.3, End: 3.6},
		{Word: "answer", Start: 4, End: 4.5},
	}
	result := Metrics(words, 10, 5)
	if result.SpeechRateWPM != 24 || result.ArticulationRateWPM != 48 {
		t.Fatalf("unexpected rates: %+v", result)
	}
	if result.LongPauseCount != 1 || result.FillerCount != 2 {
		t.Fatalf("unexpected pauses/fillers: %+v", result)
	}
}
