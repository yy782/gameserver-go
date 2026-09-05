package rpc

import (
	"context"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gameserver/api/pb"
)

// MySQLClient MySQL 数据库客户端
type MySQLClient struct {
	db *gorm.DB
}

// Player 玩家数据模型
type Player struct {
	PlayerID int64  `gorm:"primaryKey"`
	Account  string `gorm:"unique;not null"`
	Password string `gorm:"not null"`
	Salt     string
	Name     string
	Level    int32
	Exp      int64
	Gold     int64
	ServerID int32
	Score    int32
}

// TableName 指定表名
func (Player) TableName() string {
	return "players"
}

// NewMySQLClient 创建 MySQL 客户端
func NewMySQLClient(dsn string) (*MySQLClient, error) {
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	return &MySQLClient{db: db}, nil
}

// CreatePlayer 创建新玩家
func (mc *MySQLClient) CreatePlayer(ctx context.Context, account, passwordHash, salt, name string) (*pb.PlayerBase, error) {
	playerID := int64(1) // TODO: 改为实际生成
	player := &Player{
		PlayerID: playerID,
		Account:  account,
		Password: passwordHash,
		Salt:     salt,
		Name:     name,
		Level:    1,
		Exp:      0,
		Gold:     0,
		ServerID: 1,
		Score:    0,
	}

	if err := mc.db.WithContext(ctx).Create(player).Error; err != nil {
		return nil, err
	}

	return &pb.PlayerBase{
		PlayerId: playerID,
		Name:     name,
		Level:    1,
		Score:    0,
	}, nil
}

// GetPlayer 根据账号查询玩家
func (mc *MySQLClient) GetPlayer(ctx context.Context, account string) (*Player, error) {
	var player Player
	if err := mc.db.WithContext(ctx).Where("account = ?", account).First(&player).Error; err != nil {
		return nil, err
	}
	return &player, nil
}

// GetPlayerByID 根据玩家ID查询玩家
func (mc *MySQLClient) GetPlayerByID(ctx context.Context, playerID int64) (*Player, error) {
	var player Player
	if err := mc.db.WithContext(ctx).Where("player_id = ?", playerID).First(&player).Error; err != nil {
		return nil, err
	}
	return &player, nil
}

// UpdatePlayerScore 更新玩家分数
func (mc *MySQLClient) UpdatePlayerScore(ctx context.Context, playerID int64, scoreInc int32) error {
	return mc.db.WithContext(ctx).Model(&Player{}).Where("player_id = ?", playerID).
		Update("score", gorm.Expr("score + ?", scoreInc)).Error
}