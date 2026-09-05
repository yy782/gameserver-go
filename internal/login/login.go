package login

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"gameserver/api/pb"
	"gameserver/internal/common"
	"gameserver/internal/rpc"
	"strconv"
)

// token 有效期（秒）：网关断线重连、center 校验均依赖该会话
const tokenExpireSec = 600

// Login 登录服务器
type Login struct {
	pb.UnimplementedLoginServiceServer

	mysqlClient *rpc.MySQLClient
	redis       *rpc.RedisClient
}

// NewLogin 创建登录服务
func NewLogin(mysqlClient *rpc.MySQLClient, redis *rpc.RedisClient) *Login {
	return &Login{
		mysqlClient: mysqlClient,
		redis:       redis,
	}
}

// Authenticate 身份验证
func (l *Login) Authenticate(ctx context.Context, req *pb.AuthReq) (*pb.AuthRsp, error) {
	if req.Account == "" || req.Password == "" {
		return &pb.AuthRsp{Ok: false, Reason: "账号或密码不能为空"}, nil
	}

	// 查询用户
	player, err := l.mysqlClient.GetPlayer(ctx, req.Account)
	if err != nil {
		return &pb.AuthRsp{
			Ok:     false,
			Reason: "账号不存在",
		}, nil
	}

	// 验证密码（加盐哈希比对，规则 = HashHex(salt + password)，与 C++ 一致）
	passwordHash := common.HashWithSalt(req.Password, player.Salt)
	if passwordHash != player.PasswordHash {
		common.Warn("[login] 密码错误: account=%s", req.Account)
		return &pb.AuthRsp{
			Ok:     false,
			Reason: "密码错误",
		}, nil
	}

	// 签发会话 token 并缓存玩家热数据
	pbPlayer := toPlayerBase(player)
	token, ok := l.issueSession(ctx, pbPlayer)
	if !ok {
		return &pb.AuthRsp{Ok: false, Reason: "会话服务异常"}, nil
	}

	common.Info("[login] 玩家登录成功: player_id=%d name=%s", pbPlayer.PlayerId, pbPlayer.Name)
	return &pb.AuthRsp{Ok: true, Player: pbPlayer, Token: token}, nil
}

// Register 玩家注册（注册成功后直接签发 token，自动登录）
func (l *Login) Register(ctx context.Context, req *pb.RegisterReq) (*pb.RegisterRsp, error) {
	if req.Account == "" || req.Password == "" {
		return &pb.RegisterRsp{Ok: false, Reason: "账号或密码不能为空"}, nil
	}

	// 检查账号是否已存在
	if _, err := l.mysqlClient.GetPlayer(ctx, req.Account); err == nil {
		return &pb.RegisterRsp{
			Ok:     false,
			Reason: "账号已存在",
		}, nil
	}

	// 生成盐值并计算加盐哈希
	salt := common.GenerateSalt()
	passwordHash := common.HashWithSalt(req.Password, salt)

	// 确定昵称
	name := req.Name
	if name == "" {
		name = req.Account
	}

	// 创建玩家
	pbPlayer, err := l.mysqlClient.CreatePlayer(ctx, req.Account, passwordHash, salt, name)
	if err != nil {
		return &pb.RegisterRsp{
			Ok:     false,
			Reason: "注册失败",
		}, nil
	}

	// 签发会话 token
	token, ok := l.issueSession(ctx, pbPlayer)
	if !ok {
		return &pb.RegisterRsp{Ok: false, Reason: "会话服务异常"}, nil
	}

	common.Info("[login] 新玩家注册: account=%s player_id=%d name=%s",
		req.Account, pbPlayer.PlayerId, name)
	return &pb.RegisterRsp{Ok: true, Player: pbPlayer, Token: token}, nil
}

// issueSession 签发会话：token 写 Redis（SETEX token:{token} 600 = player_id），
// 玩家热数据写 player:{id}:info（TTL 3600），供 center/rank 补全信息。
func (l *Login) issueSession(ctx context.Context, player *pb.PlayerBase) (string, bool) {
	token := generateToken()

	pidStr := strconv.FormatInt(player.PlayerId, 10)
	if err := l.redis.SetEx(ctx, "token:"+token, pidStr, tokenExpireSec); err != nil {
		common.Error("[login] 写入 token 失败: %v", err)
		return "", false
	}

	infoKey := "player:" + pidStr + ":info"
	_ = l.redis.HSet(ctx, infoKey, "name", player.Name)
	_ = l.redis.HSet(ctx, infoKey, "level", strconv.Itoa(int(player.Level)))
	_ = l.redis.HSet(ctx, infoKey, "score", strconv.Itoa(int(player.Score)))
	_ = l.redis.Expire(ctx, infoKey, 3600)

	return token, true
}

// toPlayerBase 数据库行 -> protobuf 玩家信息
func toPlayerBase(p *rpc.Player) *pb.PlayerBase {
	return &pb.PlayerBase{
		PlayerId: p.ID,
		Name:     p.Name,
		Level:    p.Level,
		Exp:      p.Exp,
		Gold:     p.Gold,
		ServerId: p.ServerID,
		Score:    p.Score,
	}
}

// generateToken 生成会话 token（随机 16 字节 hex，足够长且难猜）
func generateToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// 极端情况下回退到时间戳，保证可用
		return strconv.FormatInt(common.NowMs(), 10)
	}
	return hex.EncodeToString(buf)
}
