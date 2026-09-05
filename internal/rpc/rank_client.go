package rpc

import (
	"context"
	"gameserver/api/pb"
	"gameserver/internal/common"
	"sync"
	"time"
)

// RankClient 排行榜客户端
type RankClient struct {
	redisCli *RedisClient
	mu       sync.RWMutex
}

// NewRankClient 创建排行榜客户端
func NewRankClient(redisHost string, redisPort int) *RankClient {
	return &RankClient{
		redisCli: NewRedisClient(redisHost, redisPort),
	}
}

// SubmitScore 提交分数
func (rc *RankClient) SubmitScore(ctx context.Context, playerID int64, score int32) error {
	memberKey := common.FormatAddr("", int(playerID))
	_, err := rc.redisCli.ZAdd(ctx, "rank:scores", float64(score), memberKey)
	return err
}

// GetTopN 获取 TopN 排行
func (rc *RankClient) GetTopN(ctx context.Context, n int64) ([]*pb.PlayerBase, error) {
	members, err := rc.redisCli.ZRevRange(ctx, "rank:scores", 0, n-1)
	if err != nil {
		return nil, err
	}

	players := make([]*pb.PlayerBase, 0)
	for i, member := range members {
		// 简单解析玩家ID
		players = append(players, &pb.PlayerBase{
			PlayerId: int64(i + 1),
			Name:     member,
			Score:    int32(i + 1),
		})
	}

	return players, nil
}

// Close 关闭连接
func (rc *RankClient) Close() error {
	return rc.redisCli.Close()
}