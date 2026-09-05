package login

import (
	"context"
	"crypto/rand"
	"fmt"
	"gameserver/api/pb"
	"gameserver/internal/common"
	"gameserver/internal/rpc"
	"math/big"
)

// Login 登录服务器
type Login struct {
	pb.UnimplementedLoginServiceServer

	mysqlClient *rpc.MySQLClient
	centerAddr  string
}

// NewLogin 创建登录服务
func NewLogin(mysqlClient *rpc.MySQLClient) *Login {
	return &Login{
		mysqlClient: mysqlClient,
	}
}

// Authenticate 身份验证
func (l *Login) Authenticate(ctx context.Context, req *pb.AuthReq) (*pb.AuthRsp, error) {
	// 查询用户
	player, err := l.mysqlClient.GetPlayer(ctx, req.Account)
	if err != nil {
		return &pb.AuthRsp{
			Ok:     false,
			Reason: "账号不存在",
		}, nil
	}

	// 验证密码
	passwordHash := common.HashWithSalt(req.Password, player.Salt)
	if passwordHash != player.Password {
		return &pb.AuthRsp{
			Ok:     false,
			Reason: "密码错误",
		}, nil
	}

	// 生成 Token
	token := generateToken()

	return &pb.AuthRsp{
		Ok: true,
		Player: &pb.PlayerBase{
			PlayerId: player.PlayerID,
			Name:     player.Name,
			Level:    player.Level,
			Exp:      player.Exp,
			Gold:     player.Gold,
			ServerId: player.ServerID,
			Score:    player.Score,
		},
		Token: token,
	}, nil
}

// Register 玩家注册
func (l *Login) Register(ctx context.Context, req *pb.RegisterReq) (*pb.RegisterRsp, error) {
	// 检查账号是否已存在
	_, err := l.mysqlClient.GetPlayer(ctx, req.Account)
	if err == nil {
		return &pb.RegisterRsp{
			Ok:     false,
			Reason: "账号已存在",
		}, nil
	}

	// 生成盐值
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

	// 生成 Token
	token := generateToken()

	return &pb.RegisterRsp{
		Ok:     true,
		Player: pbPlayer,
		Token:  token,
	}, nil
}

// generateToken 生成 Token
func generateToken() string {
	randomBytes, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	return fmt.Sprintf("%d_%d", common.NowMs(), randomBytes.Int64())
}