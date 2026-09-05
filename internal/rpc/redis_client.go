package rpc

import (
	"context"
	"github.com/go-redis/redis/v8"
	"gameserver/internal/common"
	"time"
)

// RedisClient Redis 客户端包装
type RedisClient struct {
	client *redis.Client
}

// NewRedisClient 创建 Redis 客户端
func NewRedisClient(host string, port int) *RedisClient {
	client := redis.NewClient(&redis.Options{
		Addr:     common.FormatAddr(host, port),
		Password: "",
		DB:       0,
	})

	return &RedisClient{
		client: client,
	}
}

// Ping 测试连接
func (rc *RedisClient) Ping(ctx context.Context) bool {
	status := rc.client.Ping(ctx)
	return status.Err() == nil
}

// SetEx 设置字符串值（带过期时间）
func (rc *RedisClient) SetEx(ctx context.Context, key, value string, expireSec int64) error {
	return rc.client.Set(ctx, key, value, time.Duration(expireSec)*time.Second).Err()
}

// Get 获取字符串值
func (rc *RedisClient) Get(ctx context.Context, key string) (string, error) {
	return rc.client.Get(ctx, key).Result()
}

// Del 删除键
func (rc *RedisClient) Del(ctx context.Context, key string) error {
	return rc.client.Del(ctx, key).Err()
}

// Expire 设置键过期时间
func (rc *RedisClient) Expire(ctx context.Context, key string, expireSec int64) error {
	return rc.client.Expire(ctx, key, time.Duration(expireSec)*time.Second).Err()
}

// HSet 哈希表设置字段
func (rc *RedisClient) HSet(ctx context.Context, key, field, value string) error {
	return rc.client.HSet(ctx, key, field, value).Err()
}

// HGet 哈希表获取字段
func (rc *RedisClient) HGet(ctx context.Context, key, field string) (string, error) {
	return rc.client.HGet(ctx, key, field).Result()
}

// HDel 哈希表删除字段
func (rc *RedisClient) HDel(ctx context.Context, key, field string) error {
	return rc.client.HDel(ctx, key, field).Err()
}

// ZIncrBy 有序集合增加分数
func (rc *RedisClient) ZIncrBy(ctx context.Context, key string, delta float64, member string) (float64, error) {
	return rc.client.ZIncrBy(ctx, key, delta, member).Result()
}

// ZRevRange 有序集合倒序范围查询
func (rc *RedisClient) ZRevRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	return rc.client.ZRevRange(ctx, key, start, stop).Result()
}

// ZRevRank 有序集合倒序排名
func (rc *RedisClient) ZRevRank(ctx context.Context, key, member string) (int64, error) {
	val := rc.client.ZRevRank(ctx, key, member)
	if val.Err() != nil && val.Err() == redis.Nil {
		return -1, nil
	}
	return val.Val(), val.Err()
}

// ZRem 有序集合删除成员
func (rc *RedisClient) ZRem(ctx context.Context, key, member string) error {
	return rc.client.ZRem(ctx, key, member).Err()
}

// ZAdd 有序集合添加成员
func (rc *RedisClient) ZAdd(ctx context.Context, key string, score float64, member string) error {
	return rc.client.ZAdd(ctx, key, &redis.Z{Score: score, Member: member}).Err()
}

// ZRangeByScore 有序集合按分数范围查询
func (rc *RedisClient) ZRangeByScore(ctx context.Context, key string, min, max float64) ([]string, error) {
	return rc.client.ZRangeByScore(ctx, key, &redis.ZRangeBy{
		Min: "-inf",
		Max: "+inf",
	}).Result()
}

// Incr 原子递增
func (rc *RedisClient) Incr(ctx context.Context, key string) (int64, error) {
	return rc.client.Incr(ctx, key).Result()
}

// Close 关闭连接
func (rc *RedisClient) Close() error {
	return rc.client.Close()
}