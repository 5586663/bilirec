package recorder

import (
	"bytes"
	"testing"
)

type closeTracker struct {
	*bytes.Reader
	closed bool
}

func (c *closeTracker) Close() error {
	c.closed = true
	return nil
}

func TestDrainAndCloseBody(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 2048)
	tr := &closeTracker{Reader: bytes.NewReader(payload)}
	drainAndCloseBody(tr)
	if !tr.closed {
		t.Fatal("expected body to be closed")
	}
	if tr.Len() != 0 {
		t.Fatalf("expected body to be drained, remaining %d", tr.Len())
	}

	drainAndCloseBody(nil)
}

func TestIsInsufficientDiskSpace(t *testing.T) {
	tests := []struct {
		name    string
		free    uint64
		minimum int64
		want    bool
	}{
		{name: "free space below positive minimum", free: 4, minimum: 5, want: true},
		{name: "free space meets positive minimum", free: 5, minimum: 5, want: false},
		{name: "zero disables check", free: 0, minimum: 0, want: false},
		{name: "negative disables check", free: 0, minimum: -1, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isInsufficientDiskSpace(tt.free, tt.minimum); got != tt.want {
				t.Fatalf(
					"isInsufficientDiskSpace(%d, %d) = %t, want %t",
					tt.free,
					tt.minimum,
					got,
					tt.want,
				)
			}
		})
	}
}
