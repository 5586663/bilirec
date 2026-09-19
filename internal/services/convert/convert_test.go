package convert

import (
	"context"
	"strings"
	"testing"
)

func TestCheckOriginalFileM4ASkipsVideoValidation(t *testing.T) {
	svc := &Service{ctx: context.Background()}

	_, err := svc.checkOriginalFile("missing-audio-only.flv", false)
	if err == nil {
		t.Fatal("expected the audio validation to fail for a missing input")
	}
	if !strings.Contains(err.Error(), "音频流") {
		t.Fatalf("error = %q, want an audio-stream validation error", err)
	}
	if strings.Contains(err.Error(), "视频流") {
		t.Fatalf("error = %q, audio-only validation should skip video validation", err)
	}
}

func TestAllowConvertDuringRecording(t *testing.T) {
	tests := []struct {
		name       string
		actives    int
		allow      bool
		maxActives int
		want       bool
	}{
		{name: "no active recordings", actives: 0, allow: false, maxActives: 1, want: true},
		{name: "recording disallowed", actives: 2, allow: false, maxActives: 1, want: false},
		{name: "recording allowed within threshold", actives: 1, allow: true, maxActives: 1, want: true},
		{name: "recording allowed above threshold", actives: 2, allow: true, maxActives: 1, want: false},
		{name: "recording allowed with threshold disabled", actives: 3, allow: true, maxActives: 0, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := allowConvertDuringRecording(tt.actives, tt.allow, tt.maxActives); got != tt.want {
				t.Fatalf("allowConvertDuringRecording(%d, %v, %d) = %v, want %v", tt.actives, tt.allow, tt.maxActives, got, tt.want)
			}
		})
	}
}
