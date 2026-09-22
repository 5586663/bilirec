package subcheck

import (
	"context"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/internal/services/notify"
	"github.com/bilirec/bilirec/internal/services/recorder"
	"github.com/bilirec/bilirec/internal/services/room"
	"github.com/bilirec/bilirec/internal/services/subscribe"
	"github.com/bilirec/bilirec/pkg/coordinator"
	"github.com/bilirec/bilirec/pkg/db"
	"github.com/bilirec/bilirec/pkg/fp"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/puzpuzpuz/xsync/v4"
	"go.uber.org/fx"
)

// =============================================================================
// 修改说明
//
// 本文件基于 bilirec v0.2.0 官方源码修改，修复「陈旧 sessionKey 导致停止录制」问题。
//
// 应用了三处改动：
//   补丁 1（start 函数）：启动时清空历史 sessionKey
//   补丁 2（tryStartShardAutoRecordRooms 主循环）：markSessionState 只在启动成功时调用
//   补丁 3（tryStartShardAutoRecordRooms 主循环）：可选的自愈检查，默认以注释形式保留
//
// 完整分析见同目录 01-bug-analysis.md、02-patch.md、03-build-and-deploy.md
// =============================================================================

var log = logger.Named("subcheck")

const sessionKeysBucketName = "SubCheck_LiveStates"
// sessionKeyGracePeriod 是 sessionKey 写入后的保护窗口。
// Start 是异步的：它可能先返回 ErrRecordingPending（录制正在启动中），
// 此时 recorder 的 GetStatus 仍是 Idle。若立刻做孤立检查会误清刚写入的
// sessionKey，导致下一轮重复 Start 甚至丢失录制。宽限期需大于一次
// subcheck tick（默认 60s），取 90s 留出余量。
const sessionKeyGracePeriod = 90 * time.Second

type Service struct {
	subSvc      *subscribe.Service
	roomSvc     *room.Service
	recSvc      *recorder.Service
	notifySvc   *notify.Service
	m           *metrics.Exporter
	bucket      *db.Bucket
	sessionKeys *xsync.Map[int, string]
	sessionKeyTimes *xsync.Map[int, int64]
	coordinator *coordinator.RoundRobin
	shardCount  int
	shardStops  []func()

	checkInterval  time.Duration
	scheduleParams scheduleParams
	lastRescale    time.Time
	scheduleMu     sync.Mutex
	jitterSecs     int

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewService(lc fx.Lifecycle, cfg *config.Config, subSvc *subscribe.Service, roomSvc *room.Service, recSvc *recorder.Service, notifySvc *notify.Service, m *metrics.Exporter) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		subSvc:      subSvc,
		roomSvc:     roomSvc,
		recSvc:      recSvc,
		notifySvc:   notifySvc,
		m:           m,
		sessionKeys: xsync.NewMap[int, string](),
		sessionKeyTimes: xsync.NewMap[int, int64](),
		ctx:         ctx,
		cancel:      cancel,
	}

	lc.Append(fx.StartStopHook(
		func() error { return s.start(cfg) },
		s.stop,
	))
	return s
}

func (s *Service) start(cfg *config.Config) error {
	client, err := db.Open(cfg.DatabaseDir + string(os.PathSeparator) + "subcheck.db")
	if err != nil {
		return err
	}
	bucket, err := client.Bucket(sessionKeysBucketName)
	if err != nil {
		return err
	}
	s.bucket = bucket

	// -------------------------------------------------------------------------
	// 【补丁 1】启动时清空历史 sessionKey
	//
	// 背景：sessionKey 的语义是「这一场直播我已处理过」，用于避免同一场直播
	// 重复启动录制。但进程重启后 recorder 的运行时状态（正在录制的 goroutine）
	// 全部归零，若 sessionKeys 从磁盘无条件恢复，就会出现
	// 「内存里没在录、磁盘说录过了」的错配，导致重启后所有仍在播的房间
	// 被 continue 跳过，永远不再录制。
	//
	// 修复：启动时清空 db 中的所有 sessionKey，让内存状态与磁盘状态从同一起点
	// 开始。凡是在播的房间，下一轮 subcheck 都会重新触发 Start。
	//
	// 完整分析见：fork 主仓十重编.so/01-bug-analysis.md
	// 补丁说明见：fork 主仓十重编.so/02-patch.md 补丁 1
	// -------------------------------------------------------------------------
	var staleKeys [][]byte
	if err := bucket.ForEach(func(k, v []byte) error {
		staleKeys = append(staleKeys, append([]byte(nil), k...))
		return nil
	}); err != nil {
		return err
	}
	if len(staleKeys) > 0 {
		log.Infof("清理启动前的 %d 条陈旧 sessionKey", len(staleKeys))
	}
	for _, k := range staleKeys {
		if err := bucket.Delete(k); err != nil {
			log.Warnf("清理陈旧 sessionKey 失败 key=%s: %v", string(k), err)
		}
	}

	s.scheduleParams = scheduleParamsFromConfig(
		cfg.SubcheckRoomsPerShard,
		cfg.SubcheckTickSecs,
		cfg.SubcheckMinIntervalSecs,
		cfg.SubcheckMaxIntervalSecs,
		cfg.SubcheckMaxShards,
	)
	s.jitterSecs = cfg.SubcheckJitterSecs
	roomCount, err := s.countLiveCheckRooms()
	if err != nil {
		log.Warnf("启动时统计订阅检查房间数失败：%v", err)
		roomCount = 0
	}
	sched := computeSchedule(roomCount, s.scheduleParams)
	s.shardCount = sched.shards
	s.checkInterval = sched.interval
	log.Infof("subcheck 调度：rooms=%d shards=%d interval=%s", roomCount, sched.shards, sched.interval)

	s.coordinator = coordinator.NewRoundRobin(s.checkInterval)
	// Keep one shard tick responsive when shard count is large.
	s.coordinator.SetMinTick(time.Second)

	s.wg.Add(1)
	go s.loop()
	return nil
}

func (s *Service) stop() error {
	s.cancel()
	s.wg.Wait()
	return s.bucket.Close()
}

func (s *Service) loop() {
	defer s.wg.Done()

	// Run one full check cycle at startup so behavior matches previous implementation.
	s.tryStartAllAutoRecordRooms()

	maxShards := s.scheduleParams.maxShards
	s.shardStops = make([]func(), 0, maxShards)
	for shard := 0; shard < maxShards; shard++ {
		ch, unregister := s.coordinator.Register(nil)
		s.shardStops = append(s.shardStops, unregister)
		s.wg.Add(1)
		go s.shardLoop(shard, ch)
	}

	<-s.ctx.Done()
	for _, stop := range s.shardStops {
		stop()
	}
	s.shardStops = nil
}

func (s *Service) shardLoop(shard int, ch <-chan struct{}) {
	defer s.wg.Done()
	for {
		select {
		case <-ch:
			if s.jitterSecs > 0 {
				time.Sleep(time.Duration(rand.Intn(s.jitterSecs)) * time.Second)
			}
			if shard == 0 {
				s.maybeRescale()
			}
			s.scheduleMu.Lock()
			activeShards := s.shardCount
			s.scheduleMu.Unlock()
			if shard >= activeShards {
				continue
			}
			s.tryStartShardAutoRecordRooms(shard, activeShards)
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *Service) tryStartAllAutoRecordRooms() {
	s.tryStartShardAutoRecordRooms(0, 1)
}

func needsLiveAction(cfg *subscribe.RoomConfig) bool {
	return cfg != nil && (cfg.Notify || cfg.AutoRecord)
}

func partitionShardRooms(rooms map[int]*subscribe.RoomConfig, shardIndex, shardCount int) (flaggedIDs, cachedIDs []int, shardRooms map[int]*subscribe.RoomConfig) {
	shardRooms = rooms
	if shardCount > 1 {
		shardRooms = fp.FilterByKey(rooms, func(roomID int) bool {
			return roomID%shardCount == shardIndex
		})
	}

	for roomID, cfg := range shardRooms {
		if needsLiveAction(cfg) {
			flaggedIDs = append(flaggedIDs, roomID)
		} else if cfg != nil {
			cachedIDs = append(cachedIDs, roomID)
		}
	}
	return flaggedIDs, cachedIDs, shardRooms
}

func (s *Service) tryStartShardAutoRecordRooms(shardIndex, shardCount int) {
	if shardCount <= 0 {
		shardCount = 1
	}

	rooms, err := s.subSvc.ListSubscribedRoomsWithConfig()
	if err != nil {
		log.Warnf("列出房间订阅失败：%v", err)
		return
	}

	flaggedIDs, cachedIDs, shardRooms := partitionShardRooms(rooms, shardIndex, shardCount)
	roomInfos := s.getNotifyRoomInfos(flaggedIDs)
	for roomID, info := range s.getCachedRoomInfos(cachedIDs) {
		roomInfos[roomID] = info
	}

	// Stale room cleanup only needs one shard per cycle.
	if shardIndex == 0 {
		s.invalidateStaleRooms(rooms)
	}

	for roomID, cfg := range shardRooms {
		info, ok := roomInfos[roomID]
		if !ok || info == nil {
			continue
		}
		isLive := info.LiveStatus == 1
		currentSessionKey := resolveLiveSessionKey(info)

		// 每輪無條件更新 live_status gauge（自愈設計：重啟後不需依賴開播事件），順帶更新 room_info
		s.m.SetLiveStatus(roomID, info.Uname, isLive)

		if !isLive || currentSessionKey == "" {
			s.clearSessionState(roomID)
			continue
		}

		// -----------------------------------------------------------------
		// 【补丁 3（可选，默认关闭）】每轮核对 recorder 状态，清理孤立 sessionKey
		//
		// 适用场景：进程运行中途 recorder 内部异常停止，但 sessionKey 仍在内存
		// 与磁盘中。补丁 1、2 无法覆盖这种情况，需要此检查自愈。
		//
		// 风险：recorder 状态转换有极小窗口可能误判（Start 刚返回但状态还没变成
		// Recording），可能造成一次额外的 Start 调用（无害，recorder 内部会去重）。
		//
		// 若你的场景中 recorder 经常中途挂掉，取消下面注释即可启用。
		// 一般情况下不需要。
		// -----------------------------------------------------------------
		if _, loaded := s.sessionKeys.Load(roomID); loaded {
			status := s.recSvc.GetStatus(roomID)
			if status != recorder.Recording && status != recorder.Recovering {
				// 宽限期保护：Start 可能返回 ErrRecordingPending（异步启动中），
				// 此时 recorder 状态仍是 Idle。只有超过宽限期仍不活跃，才判定为
				// 真正的孤立 sessionKey，避免误清刚写入的记录。
				if ts, ok := s.sessionKeyTimes.Load(roomID); !ok || time.Since(time.Unix(0, ts)) >= sessionKeyGracePeriod {
					log.Warnf("房间 %d sessionKey 陈旧（录制未激活），清理并重试", roomID)
					s.clearSessionState(roomID)
				}
			}
		}

		storedSessionKey, loaded := s.sessionKeys.Load(roomID)
		if loaded && storedSessionKey == currentSessionKey {
			continue
		}

		log.Debugf("new live session detected for room %d (%s), key: %s", roomID, info.Uname, currentSessionKey)
		s.m.LiveSessionDetected(roomID)
		state := notify.LiveStateLiveDetected

		// -----------------------------------------------------------------
		// 【补丁 2】started 标记：只有真正开始录制（或已经在录）时才写 sessionKey
		//
		// 背景：原逻辑无条件调用 markSessionState，导致 Start 失败时（并发满、
		// 磁盘满、临时网络抖动等）也写入 sessionKey，下一轮被 continue 跳过，
		// 造成「一次失败 = 永久放弃」。
		//
		// 修复：用 started 标记记录本次是否真的处理过，只有 started 为 true 时
		// 才写入 sessionKey。Start 失败时不写，下一轮 subcheck 会重新尝试。
		//
		// 补丁说明见：fork 主仓十重编.so/02-patch.md 补丁 2
		// -----------------------------------------------------------------
		started := false

		if cfg != nil && cfg.AutoRecord {
			status := s.recSvc.GetStatus(roomID)
			if status != recorder.Recording && status != recorder.Recovering {
				// Resolve duration from subscription config: -1 = unlimited, >0 = custom minutes.
				var autoRecordArgs []recorder.RecordStartOption
				switch {
				case cfg.RecordDurationMinutes == -1:
					autoRecordArgs = append(autoRecordArgs, recorder.WithDuration(0))
				case cfg.RecordDurationMinutes > 0:
					autoRecordArgs = append(autoRecordArgs, recorder.WithDuration(time.Duration(cfg.RecordDurationMinutes)*time.Minute))
				}

				streamOptions := streamOptionsFromRoomConfig(cfg)
				if len(streamOptions) > 0 {
					autoRecordArgs = append(autoRecordArgs, recorder.WithStreamOptions(streamOptions...))
				}
				if cfg.RecordDanmaku {
					autoRecordArgs = append(autoRecordArgs, recorder.WithRecordDanmaku(true))
				}

				err := s.recSvc.Start(roomID, autoRecordArgs...)
				switch err {
				case nil, recorder.ErrRecordingStarted, recorder.ErrRecordRecovering, recorder.ErrRecordingPending:
					state = notify.LiveStateAutoRecordStarted
					started = true
					log.Infof("已开始录制房间 %d（%s）", roomID, info.Uname)
				default:
					state = notify.LiveStateAutoRecordFailed
					log.Warnf("开始录制房间 %d 失败：%v", roomID, err)
					// 关键：此处不设置 started = true，下一轮 subcheck 会重新尝试
				}
			} else {
				// 已经在录（Recording / Recovering），视为已处理
				started = true
			}
		} else {
			// 未开启 AutoRecord 的房间（只开启 Notify），也视为已处理，
			// 避免每轮重复推送同一场直播的开播通知
			started = true
		}

		if cfg != nil && cfg.Notify {
			s.notifySvc.PublishLiveState(roomID, info.Uname, info.Title, state)
		}

		// 只有 started 为 true 时才写入 sessionKey
		if started {
			s.markSessionState(roomID, currentSessionKey)
		}
	}
}

func streamOptionsFromRoomConfig(cfg *subscribe.RoomConfig) []bilibili.GetStreamURLsOption {
	if cfg == nil {
		return nil
	}

	var opts []bilibili.GetStreamURLsOption
	if cfg.Qn > 0 {
		qn := bilibili.Quality(cfg.Qn)
		if qn.IsValid() {
			opts = append(opts, bilibili.WithQn(qn))
		}
	}
	if cfg.OnlyAudio {
		opts = append(opts, bilibili.WithOnlyAudio(true))
	}
	if profiles, err := bilibili.NormalizeStreamProfiles(cfg.StreamProfiles); err == nil && len(profiles) > 0 {
		opts = append(opts, bilibili.WithProfiles(profiles...))
	}
	return opts
}

func (s *Service) markSessionState(roomID int, sessionKey string) {
	s.sessionKeys.Store(roomID, sessionKey)
	s.sessionKeyTimes.Store(roomID, time.Now().UnixNano())
	if err := s.bucket.Put([]byte(strconv.Itoa(roomID)), []byte(sessionKey)); err != nil {
		log.Warnf("保存房间 %d 会话密钥失败：%v", roomID, err)
	}
}

func (s *Service) clearSessionState(roomID int) {
	s.sessionKeyTimes.Delete(roomID)
	_, loaded := s.sessionKeys.LoadAndDelete(roomID)
	if !loaded {
		return
	}
	if err := s.bucket.Delete([]byte(strconv.Itoa(roomID))); err != nil {
		log.Warnf("清理房间 %d 会话状态失败：%v", roomID, err)
	}
}

func (s *Service) invalidateStaleRooms(rooms map[int]*subscribe.RoomConfig) {
	staleRooms := make([]int, 0)
	s.sessionKeys.Range(func(key int, value string) bool {
		if _, ok := rooms[key]; !ok {
			staleRooms = append(staleRooms, key)
		}
		return true
	})
	for _, roomID := range staleRooms {
		s.clearSessionState(roomID)
		s.m.UnregisterLiveRoom(roomID)
		log.Debugf("removed stale session state for room: %v", roomID)
	}
}

func (s *Service) getCachedRoomInfos(roomIDs []int) map[int]*bilibili.LiveRoomInfoDetail {
	out := make(map[int]*bilibili.LiveRoomInfoDetail, len(roomIDs))
	if len(roomIDs) == 0 {
		return out
	}
	infos, err := s.roomSvc.GetMultipleRoomInfos(roomIDs...)
	if err != nil {
		log.Warnf("读取房间信息缓存失败：%v", err)
		return out
	}
	for _, roomID := range roomIDs {
		if info, ok := infos[strconv.Itoa(roomID)]; ok && info != nil {
			out[roomID] = info
		}
	}
	return out
}

func (s *Service) getNotifyRoomInfos(liveCheckRoomIDs []int) map[int]*bilibili.LiveRoomInfoDetail {
	notifyRoomInfos := make(map[int]*bilibili.LiveRoomInfoDetail)

	if len(liveCheckRoomIDs) > 0 {
		infos, err := s.roomSvc.RefreshRoomInfos(liveCheckRoomIDs...)
		if err != nil {
			log.Warnf("强制刷新房间信息失败：%v，回退到逐房间检查", err)
			for _, roomID := range liveCheckRoomIDs {
				one, checkErr := s.roomSvc.RefreshRoomInfos(roomID)
				if checkErr != nil {
					log.Warnf("获取房间 %d 信息失败：%v", roomID, checkErr)
					continue
				}
				if info, ok := one[strconv.Itoa(roomID)]; ok {
					notifyRoomInfos[roomID] = info
				}
			}
		} else {
			for _, roomID := range liveCheckRoomIDs {
				if info, ok := infos[strconv.Itoa(roomID)]; ok && info != nil {
					notifyRoomInfos[roomID] = info
				}
			}
		}
	}

	return notifyRoomInfos
}

func resolveLiveSessionKey(info *bilibili.LiveRoomInfoDetail) string {
	if info == nil {
		return ""
	}
	if info.LiveIDStr != "" && info.LiveIDStr != "0" {
		return "live_id_str:" + info.LiveIDStr
	}
	if info.LiveID > 0 {
		return "live_id:" + strconv.FormatInt(info.LiveID, 10)
	}
	if info.LiveTime != "" && info.LiveTime != "0000-00-00 00:00:00" {
		return "live_time:" + info.LiveTime
	}
	return ""
}
