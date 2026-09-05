package rank

import (
	"context"
	"gameserver/api/pb"
	"gameserver/internal/common"
	"gameserver/internal/rpc"
	"strconv"
)

// 排行榜 Redis Key：member=player_id，score 只增不减（ZINCRBY）
const rankKey = "rank:global"

// RankService 排行榜服务
type RankService struct {
	pb.UnimplementedRankServiceServer

	redis *rpc.RedisClient
}

// NewRankService 创建排行榜服务
func NewRankService(redis *rpc.RedisClient) *RankService {
	return &RankService{
		redis: redis,
	}
}

// SubmitScore 提交一场战斗的结算分数（胜 +10 / 负 +1，累加）
func (rs *RankService) SubmitScore(ctx context.Context, req *pb.ScoreReq) (*pb.ScoreRsp, error) {
	member := strconv.FormatInt(req.PlayerId, 10)

	newScore, err := rs.redis.ZIncrBy(ctx, rankKey, float64(req.Score), member)
	if err != nil {
		common.Error("[rank] ZINCRBY 失败: %v", err)
		return &pb.ScoreRsp{Ok: false}, nil
	}

	// 当前名次：ZREVRANK（0 基）+ 1
	rank := int32(-1)
	if zrank, err := rs.redis.ZRevRank(ctx, rankKey, member); err == nil && zrank >= 0 {
		rank = int32(zrank) + 1
	}

	common.Info("[rank] Player %d submitted score %d, now=%d rank %d",
		req.PlayerId, req.Score, int64(newScore), rank)
	return &pb.ScoreRsp{Ok: true, Rank: rank}, nil
}

// GetTopN 获取 TopN 排行
func (rs *RankService) GetTopN(ctx context.Context, req *pb.TopNReq) (*pb.TopNRsp, error) {
	n := int64(req.N)
	if n <= 0 {
		n = 10
	}
	if n > 100 {
		n = 100
	}

	entries, err := rs.redis.ZRevRangeWithScores(ctx, rankKey, 0, n-1)
	if err != nil {
		common.Error("[rank] ZREVRANGE 失败: %v", err)
		return &pb.TopNRsp{}, nil
	}

	players := make([]*pb.PlayerBase, 0, len(entries))
	for _, z := range entries {
		pid, err := strconv.ParseInt(z.Member.(string), 10, 64)
		if err != nil {
			continue
		}
		player := &pb.PlayerBase{
			PlayerId: pid,
			Score:    int32(z.Score),
		}
		// 补全昵称 / 等级
		infoKey := "player:" + z.Member.(string) + ":info"
		if name, err := rs.redis.HGet(ctx, infoKey, "name"); err == nil {
			player.Name = name
		}
		if level, err := rs.redis.HGet(ctx, infoKey, "level"); err == nil {
			if lv, e := strconv.Atoi(level); e == nil {
				player.Level = int32(lv)
			}
		}
		players = append(players, player)
	}

	return &pb.TopNRsp{Players: players}, nil
}
