package recording

import (
	"testing"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/services/room"
)

func ResolveLiveTestRoomID(tb testing.TB, roomSvc *room.Service) int {
	tb.Helper()
	rooms := ResolveLiveTestRoomIDs(tb, roomSvc, 1)
	if len(rooms) == 0 {
		tb.Skip("no validated live room id available")
	}
	return rooms[0]
}

func ResolveLiveTestRoomIDs(tb testing.TB, roomSvc *room.Service, required int) []int {
	tb.Helper()
	return pickLiveTestRoomIDs(tb, roomSvc, nil, required, nil)
}

func ResolveLiveTestRoomIDWithStream(tb testing.TB, sess *Session, opts ...bilibili.GetStreamURLsOption) int {
	tb.Helper()
	rooms := ResolveLiveTestRoomIDsWithStream(tb, sess, 1, opts...)
	if len(rooms) == 0 {
		tb.Skip("no validated live room id available")
	}
	return rooms[0]
}

func ResolveLiveTestRoomIDsWithStream(tb testing.TB, sess *Session, required int, opts ...bilibili.GetStreamURLsOption) []int {
	tb.Helper()
	if sess == nil || sess.Bili == nil {
		tb.Fatal("stream-aware room pick requires a recorder test session with a Bilibili client")
	}
	return pickLiveTestRoomIDs(tb, sess.Room, sess.Bili, required, opts)
}

// ResolveLiveTestRoomsForStreamSlots picks a distinct live room for each Start()
// option set. Scarce formats (TS / fMP4, extra constraints) are matched first
// so a dual-format room is not consumed by an easier slot.
func ResolveLiveTestRoomsForStreamSlots(tb testing.TB, sess *Session, slots []LiveStreamSlot) []int {
	tb.Helper()
	if sess == nil || sess.Bili == nil {
		tb.Fatal("stream-aware room pick requires a recorder test session with a Bilibili client")
	}
	if len(slots) == 0 {
		return nil
	}
	return pickLiveTestRoomIDsForSlots(tb, sess.Room, sess.Bili, slots)
}
