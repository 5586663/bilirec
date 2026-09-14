package recorder_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/services/room"
	"github.com/bilirec/bilirec/internal/testutil"
)

const (
	recorderLiveValidationMaxRetriesAfterUnableValidateAllRooms = 2
	recorderLiveValidationBackoff                               = 1500 * time.Millisecond
	recorderLiveValidationMinPool                               = 12
	recorderLiveValidationPerRoom                               = 6
	recorderLiveStreamProbeMinPool                              = 24
	recorderLiveStreamProbePerRoom                              = 10
	recorderLiveStreamProbeConcurrency                          = 3
)

func resolveLiveTestRoomID(tb testing.TB, roomSvc *room.Service) int {
	tb.Helper()
	rooms := resolveLiveTestRoomIDs(tb, roomSvc, 1)
	if len(rooms) == 0 {
		tb.Skip("no validated live room id available")
	}
	return rooms[0]
}

func resolveLiveTestRoomIDs(tb testing.TB, roomSvc *room.Service, required int) []int {
	tb.Helper()
	return pickLiveTestRoomIDs(tb, roomSvc, nil, required, nil)
}

func resolveLiveTestRoomIDWithStream(tb testing.TB, sess *recorderTestSession, opts ...bilibili.GetStreamURLsOption) int {
	tb.Helper()
	rooms := resolveLiveTestRoomIDsWithStream(tb, sess, 1, opts...)
	if len(rooms) == 0 {
		tb.Skip("no validated live room id available")
	}
	return rooms[0]
}

func resolveLiveTestRoomIDsWithStream(tb testing.TB, sess *recorderTestSession, required int, opts ...bilibili.GetStreamURLsOption) []int {
	tb.Helper()
	if sess == nil || sess.Bili == nil {
		tb.Fatal("stream-aware room pick requires a recorder test session with a Bilibili client")
	}
	return pickLiveTestRoomIDs(tb, sess.Room, sess.Bili, required, opts)
}

type liveStreamSlot struct {
	Opts     []bilibili.GetStreamURLsOption
	Scarcity int
}

func liveStreamSlotForProfile(profile bilibili.StreamProfile, extra ...bilibili.GetStreamURLsOption) liveStreamSlot {
	opts := append([]bilibili.GetStreamURLsOption{bilibili.WithProfiles(profile)}, extra...)
	scarcity := 0
	switch profile {
	case bilibili.ProfileHLSFMP4:
		scarcity = 1
	case bilibili.ProfileHLSTS:
		scarcity = 2
	}
	for _, opt := range extra {
		if opt != nil {
			scarcity++
		}
	}
	return liveStreamSlot{Opts: opts, Scarcity: scarcity}
}

func originalQualityStreamOpts() []bilibili.GetStreamURLsOption {
	return []bilibili.GetStreamURLsOption{
		bilibili.WithQn(bilibili.QualityOriginal),
	}
}

func httpFlvOriginalStreamOpts() []bilibili.GetStreamURLsOption {
	return []bilibili.GetStreamURLsOption{
		bilibili.WithProfiles(bilibili.ProfileHTTPFLV),
		bilibili.WithQn(bilibili.QualityOriginal),
	}
}

// resolveLiveTestRoomsForStreamSlots picks a distinct live room for each Start()
// option set. Scarce formats (TS / fMP4, extra constraints) are matched first
// so a dual-format room is not consumed by an easier slot.
func resolveLiveTestRoomsForStreamSlots(tb testing.TB, sess *recorderTestSession, slots []liveStreamSlot) []int {
	tb.Helper()
	if sess == nil || sess.Bili == nil {
		tb.Fatal("stream-aware room pick requires a recorder test session with a Bilibili client")
	}
	if len(slots) == 0 {
		return nil
	}
	return pickLiveTestRoomIDsForSlots(tb, sess.Room, sess.Bili, slots)
}

func pickLiveTestRoomIDs(tb testing.TB, roomSvc *room.Service, bili *bilibili.Client, required int, streamOpts []bilibili.GetStreamURLsOption) []int {
	tb.Helper()
	if required <= 0 {
		return nil
	}

	candidates := liveTestRoomCandidates(tb, required, len(streamOpts) > 0)
	return validateLiveRoomIDs(tb, roomSvc, bili, candidates, required, streamOpts)
}

func pickLiveTestRoomIDsForSlots(tb testing.TB, roomSvc *room.Service, bili *bilibili.Client, slots []liveStreamSlot) []int {
	tb.Helper()
	candidates := liveTestRoomCandidates(tb, len(slots), true)
	return validateLiveRoomIDsForSlots(tb, roomSvc, bili, candidates, slots)
}

func liveTestRoomCandidates(tb testing.TB, required int, streamProbe bool) []int {
	tb.Helper()
	if pinned := pinnedTestRoomIDsFromEnv(tb); len(pinned) > 0 {
		return pinned
	}

	minPool := recorderLiveValidationMinPool
	perRoom := recorderLiveValidationPerRoom
	if streamProbe {
		minPool = recorderLiveStreamProbeMinPool
		perRoom = recorderLiveStreamProbePerRoom
	}
	candidateCount := max(minPool, required*perRoom)
	candidates := uniqueInts(testutil.LiveRoomIDs(tb, candidateCount))
	if len(candidates) == 0 {
		tb.Skip("no candidate live room ids available")
	}
	return candidates
}

func pinnedTestRoomIDsFromEnv(tb testing.TB) []int {
	tb.Helper()
	if raw := strings.TrimSpace(os.Getenv("BILIBILI_TEST_ROOM_IDS")); raw != "" {
		return uniqueInts(parseTestRoomIDList(tb, raw, "BILIBILI_TEST_ROOM_IDS"))
	}
	if raw := strings.TrimSpace(os.Getenv("BILIBILI_TEST_ROOM_ID")); raw != "" {
		return []int{parseTestRoomID(tb, raw, "BILIBILI_TEST_ROOM_ID")}
	}
	return nil
}

func parseTestRoomIDList(tb testing.TB, raw, source string) []int {
	tb.Helper()
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
	})
	ids := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		ids = append(ids, parseTestRoomID(tb, part, source))
	}
	return ids
}

func parseTestRoomID(tb testing.TB, raw, source string) int {
	tb.Helper()
	id, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || id <= 0 {
		tb.Fatalf("invalid room id in %s: %q", source, raw)
	}
	return id
}

func validateLiveRoomIDs(tb testing.TB, roomSvc *room.Service, bili *bilibili.Client, candidates []int, required int, streamOpts []bilibili.GetStreamURLsOption) []int {
	tb.Helper()
	if len(candidates) == 0 {
		tb.Skip("no candidate live room ids available")
	}

	tb.Logf("live room validation candidates: %v stream_probe=%v", candidates, len(streamOpts) > 0)
	var attemptErrors []string

	maxRounds := recorderLiveValidationMaxRetriesAfterUnableValidateAllRooms + 1
	for round := 1; round <= maxRounds; round++ {
		liveIDs, fetchErrs := liveRoomIDsFromCandidates(tb, roomSvc, candidates, round)
		attemptErrors = append(attemptErrors, fetchErrs...)
		if len(liveIDs) == 0 {
			if round < maxRounds {
				time.Sleep(recorderLiveValidationBackoff)
			}
			continue
		}

		validated := liveIDs
		if len(streamOpts) > 0 {
			if bili == nil {
				tb.Fatal("stream URL probe requested without bilibili client")
			}
			validated = filterRoomsWithStreamURLs(tb, bili, liveIDs, required, streamOpts)
		}

		if len(validated) >= required {
			picked := validated[:required]
			tb.Logf("validated live room ids (round %d): %v", round, picked)
			return picked
		}
		attemptErrors = append(attemptErrors, fmt.Sprintf("round %d matched %d/%d live rooms (stream_probe=%v)", round, len(validated), required, len(streamOpts) > 0))
		if round < maxRounds {
			time.Sleep(recorderLiveValidationBackoff)
		}
	}

	tb.Skipf("unable to validate %d live room(s); candidates=%v; details=%s", required, candidates, strings.Join(attemptErrors, "\n"))
	return nil
}

func validateLiveRoomIDsForSlots(tb testing.TB, roomSvc *room.Service, bili *bilibili.Client, candidates []int, slots []liveStreamSlot) []int {
	tb.Helper()
	if len(candidates) == 0 {
		tb.Skip("no candidate live room ids available")
	}
	if bili == nil {
		tb.Fatal("stream URL probe requested without bilibili client")
	}

	tb.Logf("live room validation candidates: %v mixed_stream_slots=%d", candidates, len(slots))
	var attemptErrors []string

	maxRounds := recorderLiveValidationMaxRetriesAfterUnableValidateAllRooms + 1
	for round := 1; round <= maxRounds; round++ {
		liveIDs, fetchErrs := liveRoomIDsFromCandidates(tb, roomSvc, candidates, round)
		attemptErrors = append(attemptErrors, fetchErrs...)
		if len(liveIDs) == 0 {
			if round < maxRounds {
				time.Sleep(recorderLiveValidationBackoff)
			}
			continue
		}

		picked, reason := assignLiveRoomsToStreamSlots(tb, bili, liveIDs, slots)
		if len(picked) == len(slots) {
			tb.Logf("validated live room ids (round %d): %v", round, picked)
			return picked
		}
		attemptErrors = append(attemptErrors, fmt.Sprintf("round %d mixed assign failed: %s", round, reason))
		if round < maxRounds {
			time.Sleep(recorderLiveValidationBackoff)
		}
	}

	tb.Skipf("unable to validate %d live rooms with per-slot stream formats; candidates=%v; details=%s", len(slots), candidates, strings.Join(attemptErrors, "\n"))
	return nil
}

func liveRoomIDsFromCandidates(tb testing.TB, roomSvc *room.Service, candidates []int, round int) ([]int, []string) {
	tb.Helper()
	var errs []string
	roomSvc.InvalidateRooms(candidates...)
	infos, err := roomSvc.GetMultipleRoomInfos(candidates...)
	if err != nil {
		return nil, []string{fmt.Sprintf("round %d fetch error: %v", round, err)}
	}

	liveIDs := make([]int, 0, len(candidates))
	for _, roomID := range candidates {
		info, ok := infos[strconv.Itoa(roomID)]
		if !ok || info == nil {
			errs = append(errs, fmt.Sprintf("round %d room %d missing info", round, roomID))
			continue
		}
		if info.LiveStatus == 1 {
			liveIDs = append(liveIDs, roomID)
			continue
		}
		errs = append(errs, fmt.Sprintf("round %d room %d offline (live_status=%d)", round, roomID, info.LiveStatus))
	}
	return liveIDs, errs
}

func assignLiveRoomsToStreamSlots(tb testing.TB, bili *bilibili.Client, liveIDs []int, slots []liveStreamSlot) ([]int, string) {
	tb.Helper()
	unused := append([]int(nil), liveIDs...)
	picked := make([]int, len(slots))
	for _, slotIdx := range streamSlotPickOrder(slots) {
		match := filterRoomsWithStreamURLs(tb, bili, unused, 1, slots[slotIdx].Opts)
		if len(match) == 0 {
			return nil, fmt.Sprintf("no unused live room for slot %d", slotIdx)
		}
		picked[slotIdx] = match[0]
		unused = removeInt(unused, match[0])
	}
	return picked, ""
}

func streamSlotPickOrder(slots []liveStreamSlot) []int {
	order := make([]int, len(slots))
	for i := range slots {
		order[i] = i
	}
	for i := 1; i < len(order); i++ {
		j := i
		for j > 0 && slots[order[j]].Scarcity > slots[order[j-1]].Scarcity {
			order[j], order[j-1] = order[j-1], order[j]
			j--
		}
	}
	return order
}

func filterRoomsWithStreamURLs(tb testing.TB, bili *bilibili.Client, liveIDs []int, required int, opts []bilibili.GetStreamURLsOption) []int {
	tb.Helper()
	if required <= 0 || len(liveIDs) == 0 {
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sem := make(chan struct{}, recorderLiveStreamProbeConcurrency)
	var (
		mu      sync.Mutex
		matched []int
		wg      sync.WaitGroup
	)

	for _, roomID := range liveIDs {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(roomID int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}

			ok, reason := roomHasStreamURLs(bili, roomID, opts...)
			mu.Lock()
			defer mu.Unlock()
			if !ok {
				tb.Logf("live room %d skipped for requested stream: %s", roomID, reason)
				return
			}
			if len(matched) >= required {
				return
			}
			matched = append(matched, roomID)
			if len(matched) >= required {
				cancel()
			}
		}(roomID)
	}
	wg.Wait()
	if len(matched) > required {
		matched = matched[:required]
	}
	return matched
}

func roomHasStreamURLs(bili *bilibili.Client, roomID int, opts ...bilibili.GetStreamURLsOption) (bool, string) {
	urls, err := bili.GetStreamURLsV2(roomID, opts...)
	if err != nil {
		return false, fmt.Sprintf("playurl error: %v", err)
	}
	if len(urls) == 0 {
		return false, "empty playurl for requested format"
	}
	return true, ""
}

func uniqueInts(values []int) []int {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[int]struct{}, len(values))
	result := make([]int, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func removeInt(values []int, target int) []int {
	out := values[:0]
	for _, value := range values {
		if value != target {
			out = append(out, value)
		}
	}
	return out
}
