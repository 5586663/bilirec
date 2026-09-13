//go:build analyze_gift_b64

package danmaku

import (
	"context"
	"encoding/base64"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/tidwall/gjson"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

// Temporary live analysis: collect SEND_GIFT_V2 from a real room and benchmark
// stdlib encoding/base64 decode against parseGiftV2 on those payloads.
//
//	go test -tags analyze_gift_b64 ./internal/services/danmaku -run TestAnalyzeLiveGiftV2Base64 -timeout 3m -count=1 -v
const analyzeGiftV2RoomID = 1947277414

func TestAnalyzeLiveGiftV2Base64(t *testing.T) {
	t.Setenv("BILIBILI_LOGIN_MODE", "anonymous")
	t.Setenv("OUTPUT_DIR", t.TempDir())
	t.Setenv("DATABASE_DIR", t.TempDir())
	t.Setenv("SECRET_DIR", t.TempDir())

	roomID := analyzeGiftV2RoomID
	if raw := strings.TrimSpace(os.Getenv("BILIBILI_TEST_ROOM_ID")); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("BILIBILI_TEST_ROOM_ID=%q: %v", raw, err)
		}
		roomID = id
	}

	var client *bilibili.Client
	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Populate(&client),
		fx.StartTimeout(25*time.Second),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	info, err := client.GetLiveRoomInfo(roomID)
	if err != nil {
		t.Fatalf("GetLiveRoomInfo(%d): %v", roomID, err)
	}
	if info.LiveStatus != 1 {
		t.Skipf("room %d is not live (status=%d)", roomID, info.LiveStatus)
	}
	protocolRoomID := int(info.RoomID)
	t.Logf("room=%d protocol=%d uname=%s title=%q online=%d", roomID, protocolRoomID, info.Uname, info.Title, info.Online)

	danmu, err := client.GetDanmuInfo(t.Context(), protocolRoomID)
	if err != nil {
		t.Fatalf("GetDanmuInfo: %v", err)
	}
	uid, buvid := client.DanmakuIdentity()

	const (
		collectFor = 75 * time.Second
		targetV2   = 80
		handlerBuf = 512
	)
	type sample struct {
		raw     []byte
		pb      string
		decoded int
		gifts   int
	}

	v2Ch := make(chan []byte, handlerBuf)
	var v1Count, dropped atomic.Int64
	ws := bilibili.NewLiveMessageClient(protocolRoomID, uid, buvid)
	ws.HandleFunc("SEND_GIFT", func(_ []byte) { v1Count.Add(1) })
	ws.HandleFunc("SEND_GIFT_V2", func(raw []byte) {
		cp := make([]byte, len(raw))
		copy(cp, raw)
		select {
		case v2Ch <- cp:
		default:
			dropped.Add(1)
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), collectFor)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- ws.Run(ctx, danmu.HostList[0], danmu.Token) }()

	var samples []sample
	deadline := time.After(collectFor)
loop:
	for len(samples) < targetV2 {
		select {
		case raw := <-v2Ch:
			pb := strings.Clone(gjson.GetBytes(raw, "data.pb").String())
			if pb == "" {
				continue
			}
			decoded, decErr := base64.StdEncoding.DecodeString(pb)
			if decErr != nil {
				t.Logf("skip bad pb: %v", decErr)
				continue
			}
			gifts, parseErr := parseGiftV2(raw)
			if parseErr != nil {
				t.Logf("skip parse: %v", parseErr)
				continue
			}
			samples = append(samples, sample{raw: raw, pb: pb, decoded: len(decoded), gifts: len(gifts)})
		case <-deadline:
			break loop
		case err := <-errCh:
			if ctx.Err() == nil && err != nil {
				t.Fatalf("websocket: %v", err)
			}
			break loop
		}
	}
	cancel()
	ws.Close()

	t.Logf("collected v2=%d v1=%d dropped=%d window=%s", len(samples), v1Count.Load(), dropped.Load(), collectFor)
	if len(samples) < 3 {
		t.Skipf("too few SEND_GIFT_V2 messages (%d) to compare decoders", len(samples))
	}

	pbLens := make([]int, len(samples))
	decLens := make([]int, len(samples))
	giftCounts := make([]int, len(samples))
	pbs := make([]string, len(samples))
	raws := make([][]byte, len(samples))
	var totalPB, totalDecoded, totalGifts int
	for i, s := range samples {
		pbLens[i] = len(s.pb)
		decLens[i] = s.decoded
		giftCounts[i] = s.gifts
		pbs[i] = s.pb
		raws[i] = s.raw
		totalPB += len(s.pb)
		totalDecoded += s.decoded
		totalGifts += s.gifts
	}
	t.Logf("pb chars:    n=%d min=%d p50=%d p95=%d max=%d avg=%.0f", len(pbLens), minInt(pbLens), percentile(pbLens, 50), percentile(pbLens, 95), maxInt(pbLens), avgInt(pbLens))
	t.Logf("decoded bytes: min=%d p50=%d p95=%d max=%d avg=%.0f", minInt(decLens), percentile(decLens, 50), percentile(decLens, 95), maxInt(decLens), avgInt(decLens))
	t.Logf("gifts/msg: min=%d p50=%d p95=%d max=%d total=%d", minInt(giftCounts), percentile(giftCounts, 50), percentile(giftCounts, 95), maxInt(giftCounts), totalGifts)

	stdlib := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(totalPB))
		var sink int
		for b.Loop() {
			for _, pb := range pbs {
				out, err := base64.StdEncoding.DecodeString(pb)
				if err != nil {
					b.Fatal(err)
				}
				sink += len(out)
			}
		}
		_ = sink
	})
	parse := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		var sink int
		for b.Loop() {
			for _, raw := range raws {
				gifts, err := parseGiftV2(raw)
				if err != nil {
					b.Fatal(err)
				}
				sink += len(gifts)
			}
		}
		_ = sink
	})

	n := float64(len(samples))
	stdlibPer := float64(stdlib.NsPerOp()) / n
	parsePer := float64(parse.NsPerOp()) / n
	share := 100 * stdlibPer / parsePer

	t.Logf("stdlib  DecodeString: %s  (%.0f ns/msg)", stdlib, stdlibPer)
	t.Logf("parseGiftV2 (gjson+b64+pb): %s  (%.0f ns/msg)", parse, parsePer)
	t.Logf("stdlib decode is %.1f%% of parseGiftV2", share)

	const (
		sizeCutoff  = 2048
		shareCutoff = 15.0
	)
	p95 := percentile(pbLens, 95)
	switch {
	case p95 < sizeCutoff && share < shareCutoff:
		t.Logf("verdict: decode is not a bottleneck — live p95 pb is %d chars and decode is only %.1f%% of parseGiftV2", p95, share)
	default:
		t.Logf("verdict: decode is %.1f%% of parseGiftV2 (p95=%d chars)", share, p95)
	}
}

func percentile(vals []int, p int) int {
	if len(vals) == 0 {
		return 0
	}
	cp := slices.Clone(vals)
	slices.Sort(cp)
	idx := (p * (len(cp) - 1)) / 100
	return cp[idx]
}

func minInt(vals []int) int {
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func maxInt(vals []int) int {
	m := vals[0]
	for _, v := range vals[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

func avgInt(vals []int) float64 {
	sum := 0
	for _, v := range vals {
		sum += v
	}
	return float64(sum) / float64(len(vals))
}
