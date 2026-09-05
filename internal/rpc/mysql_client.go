package rpc

import (
	"context"
	"gameserver/api/pb"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// MySQLClient MySQL 数据库客户端（复用 C++ scripts/init_db.sql 的表结构）
type MySQLClient struct {
	db *gorm.DB
}

// Player 玩家数据模型，对应表 player（与 C++ 建表语句完全一致）
type Player struct {
	ID           int64  `gorm:"column:id;primaryKey"` // DB 自增
	Account      string `gorm:"column:account"`
	PasswordHash string `gorm:"column:password_hash"`
	Salt         string `gorm:"column:salt"`
	Name         string `gorm:"column:name"`
	Level        int32  `gorm:"column:level"`
	Exp          int64  `gorm:"column:exp"`
	Gold         int64  `gorm:"column:gold"`
	Score        int32  `gorm:"column:score"`
	ServerID     int32  `gorm:"column:server_id"` // DB 默认 1
}

// TableName 指定表名（与 C++ 一致，单数 player）
func (Player) TableName() string {
	return "player"
}

// NewMySQLClient 创建 MySQL 客户端
func NewMySQLClient(dsn string) (*MySQLClient, error) {
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	return &MySQLClient{db: db}, nil
}

// CreatePlayer 创建新玩家（只写 C++ 同款的 4 列，其余由 DB 默认值填充；
// 与 C++ CreatePlayer + 注册后重新 SELECT 的行为一致）
func (mc *MySQLClient) CreatePlayer(ctx context.Context, account, passwordHash, salt, name string) (*pb.PlayerBase, error) {
	player := &Player{
		Account:      account,
		PasswordHash: passwordHash,
		Salt:         salt,
		Name:         name,
	}
	if err := mc.db.WithContext(ctx).
		Omit("level", "exp", "gold", "score", "server_id").
		Create(player).Error; err != nil {
		return nil, err
	}

	// 重新查询拿到 DB 默认值（level=1 等）
	saved, err := mc.GetPlayerByID(ctx, player.ID)
	if err != nil {
		return nil, err
	}
	return saved.toPlayerBase(), nil
}

// GetPlayer 根据账号查询玩家（对应 C++ SELECT ... WHERE account=）
func (mc *MySQLClient) GetPlayer(ctx context.Context, account string) (*Player, error) {
	var player Player
	if err := mc.db.WithContext(ctx).Where("account = ?", account).First(&player).Error; err != nil {
		return nil, err
	}
	return &player, nil
}

// GetPlayerByID 根据玩家 ID 查询玩家
func (mc *MySQLClient) GetPlayerByID(ctx context.Context, playerID int64) (*Player, error) {
	var player Player
	if err := mc.db.WithContext(ctx).Where("id = ?", playerID).First(&player).Error; err != nil {
		return nil, err
	}
	return &player, nil
}

// UpdatePlayerScore 更新玩家分数（列与表结构对齐）
func (mc *MySQLClient) UpdatePlayerScore(ctx context.Context, playerID int64, scoreInc int32) error {
	return mc.db.WithContext(ctx).Model(&Player{}).Where("id = ?", playerID).
		Update("score", gorm.Expr("score + ?", scoreInc)).Error
}

func (p *Player) toPlayerBase() *pb.PlayerBase {
	return &pb.PlayerBase{
		PlayerId: p.ID,
		Name:     p.Name,
		Level:    p.Level,
		Exp:      p.Exp,
		Gold:     p.Gold,
		Score:    p.Score,
		ServerId: p.ServerID,
	}
}
