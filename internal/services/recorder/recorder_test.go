package recorder_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/services/recorder"
	"github.com/bilirec/bilirec/internal/testutil/recording"
)

func TestFlvRecord(t *testing.T) {
	recording.RunFormatRecordTest(t, bilibili.ProfileHTTPFLV, "flv")
}

func TestTsRecord(t *testing.T) {
	recording.RunFormatRecordTest(t, bilibili.ProfileHLSTS, "ts")
}

func TestFmp4Record(t *testing.T) {
	recording.RunFormatRecordTest(t, bilibili.ProfileHLSFMP4, "fmp4")
}

func TestFlvFmp4ConcurrentRecord(t *testing.T) {
	recording.RunConcurrentFormatRecordTest(t,
		recording.ConcurrentFormatRecordSpec{Profile: bilibili.ProfileHTTPFLV, Format: "flv"},
		recording.ConcurrentFormatRecordSpec{Profile: bilibili.ProfileHLSFMP4, Format: "fmp4"},
	)
}

// ZZZ_Final_* long soak tests run in isolated go test processes so heap/cpu pprof are
// not polluted by other integration tests. CI runs each in a separate workflow step; locally:
//
//	go test ./internal/services/recorder -run TestZZZ_Final_Concurrent3WayFlvRecord -count=1 -timeout 30m
//	go test ./internal/services/recorder -run TestZZZ_Final_Concurrent3WayFmp4Record -count=1 -timeout 30m
//
// Danmaku ZZZ final: go test ./internal/services/danmaku -run TestZZZ_Final_DanmakuJsonlRecord ...
//
// The ZZZ prefix keeps lexicographic order last when the full recorder package is
// run in one invocation (e.g. go test ./internal/services/recorder without -run).
//
// Optional: RECORDER_RECORD_PROFILE_INTERVAL_SECS=60s
func TestZZZ_Final_Concurrent3WayFlvRecord(t *testing.T) {
	recording.RunZZZFinalConcurrentRecordTest(t, bilibili.ProfileHTTPFLV, "flv", 3)
}

func TestZZZ_Final_Concurrent3WayFmp4Record(t *testing.T) {
	recording.RunZZZFinalConcurrentRecordTest(t, bilibili.ProfileHLSFMP4, "fmp4", 3)
}

func TestFlvRecord_AutoStopAfterDuration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping TestFlvRecord_AutoStopAfterDuration in short mode")
	}

	const recordDuration = 60 * time.Second
	const pollInterval = 2 * time.Second
	const tolerance = 20 * time.Second

	sess := recording.NewSession(t)
	room := recording.ResolveLiveTestRoomID(t, sess.Room)

	t.Logf("starting recording with duration limit: %v", recordDuration)
	startPhase, err := sess.Monitor.BeginPhase("auto_stop_start")
	if err != nil {
		t.Fatalf("begin start phase: %v", err)
	}
	startErr := sess.Recorder.Start(room, recorder.WithDuration(recordDuration))
	startReport := startPhase.End(t)
	recording.HandleRecordingStartErr(t, startErr)
	recording.LogCPUPhase(t, startReport)

	if status := sess.Recorder.GetStatus(room); status != recorder.Recording {
		t.Fatalf("expected status %q immediately after start, got %q", recorder.Recording, status)
	}

	deadline := time.Now().Add(recordDuration + tolerance)
	startTime := time.Now()
	for time.Now().Before(deadline) {
		<-time.After(pollInterval)
		status := sess.Recorder.GetStatus(room)
		t.Logf("elapsed: %v, status: %s", time.Since(startTime).Round(time.Second), status)
		if status == recorder.Idle {
			sess.Monitor.SnapshotMemory(t, "after_auto_stop", false)
			sess.Monitor.SnapshotGoroutines(t, "after_auto_stop")
			sess.Monitor.LogAnalysisHints(t)
			t.Logf("recording auto-stopped after ~%v as expected", recordDuration)
			return
		}
	}

	t.Errorf("recording did not auto-stop within %v (duration=%v + tolerance=%v)", recordDuration+tolerance, recordDuration, tolerance)
	sess.Recorder.Stop(room)
}

func TestChannelRangeReturnedWhileStreaming(t *testing.T) {
	ch := make(chan int, 10)
	send := func() {
		for i := 0; i < 10; i++ {
			ch <- i
			time.Sleep(1 * time.Second)
		}
		close(ch)
	}
	go send()
	for v := range ch {
		t.Logf("received: %d", v)
		if v == 5 {
			t.Log("stop early")
			break
		}
	}
	<-time.After(5 * time.Second)
	for v := range ch {
		t.Logf("received after first range stopped: %d", v)
	}
}

func TestInfoOutputPath_DefaultEmpty(t *testing.T) {
	info := &recorder.Info{}
	if got := info.OutputPath(); got != "" {
		t.Fatalf("expected empty output path, got %q", got)
	}
}

func TestInfoOutputPath_AtomicConcurrentReadWrite(t *testing.T) {
	info := &recorder.Info{}
	info.SetOutputPath("")

	const writers = 8
	const readers = 8
	const loops = 2000

	var wg sync.WaitGroup

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for n := 0; n < loops; n++ {
				info.SetOutputPath(fmt.Sprintf("seg-%d-%d.flv", id, n))
			}
		}(i)
	}

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < loops; n++ {
				_ = info.OutputPath()
			}
		}()
	}

	wg.Wait()

	if got := info.OutputPath(); got == "" {
		t.Fatal("expected non-empty output path after concurrent writes")
	}
}
