package recorder_test

import (
	"slices"
	"testing"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
)

func TestStreamSlotPickOrder_PrefersScarceFormats(t *testing.T) {
	t.Parallel()
	slots := []liveStreamSlot{
		liveStreamSlotForProfile(bilibili.ProfileHTTPFLV),
		liveStreamSlotForProfile(bilibili.ProfileHLSFMP4),
		liveStreamSlotForProfile(bilibili.ProfileHLSTS, bilibili.WithOnlyAudio(true)),
	}
	got := streamSlotPickOrder(slots)
	want := []int{2, 1, 0}
	if !slices.Equal(got, want) {
		t.Fatalf("pick order = %v, want %v (scarcity ts-audio=%d fmp4=%d flv=%d)",
			got, want, slots[2].Scarcity, slots[1].Scarcity, slots[0].Scarcity)
	}
}

func TestRemoveInt(t *testing.T) {
	t.Parallel()
	got := removeInt([]int{3, 8, 3, 1}, 3)
	want := []int{8, 1}
	if !slices.Equal(got, want) {
		t.Fatalf("removeInt = %v, want %v", got, want)
	}
}
