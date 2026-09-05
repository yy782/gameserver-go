package rank

import (
	"context"
	"gameserver/api/pb"
	"gameserver/internal/common"
	"gameserver/internal/rpc"
	"sort"
	"sync"
)

// RankService 排行榜服务
type RankService struct {
	pb.UnimplementedRankServiceServer

	redis *rpc.RedisClient
	mu    sync.RWMutex
	ranks map[string]int32 // 玩家ID -> 分数
}

// NewRankService 创建排行榜服务
func NewRankService(redis *rpc.RedisClient) *RankService {
	return &RankService{
		redis: redis,
		ranks: make(map[string]int32),
	}
}

func (rs *RankService) SubmitScore(ctx context.Context, req *pb.ScoreReq) (*pb.ScoreRsp, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	rs.ranks[common.FormatAddr("", int(req.PlayerId))] = req.Score

	// 计算排名
	scores := make([]int32, 0)
	for _, s := range rs.ranks {
		scores = append(scores, s)
	}
	sort.Slice(scores, func(i, j int) bool {
		return scores[i] > scores[j]
	})

	rank := int32(0)
	for i, s := range scores {
		if s == req.Score {
			rank = int32(i) + 1
			break
		}
	}

	common.Info("[rank] Player %d submitted score %d, rank %d", req.PlayerId, req.Score, rank)

	return &pb.ScoreRsp{
		Ok:   true,
		Rank: rank,
	}, nil
}

func (rs *RankService) GetTopN(ctx context.Context, req *pb.TopNReq) (*pb.TopNRsp, error) {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	type scoreEntry struct {
		id    string
		score int32
	}

	entries := make([]scoreEntry, 0)
	for id, score := range rs.ranks {
		entries = append(entries, scoreEntry{id, score})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].score > entries[j].score
	})

	n := int(req.N)
	if n > len(entries) {
		n = len(entries)
	}

	players := make([]*pb.PlayerBase, 0)
	for i := 0; i < n; i++ {
		players = append(players, &pb.PlayerBase{
			Score: entries[i].score,
		})
	}

	return &pb.TopNRsp{
		Players: players,
	}, nil
}