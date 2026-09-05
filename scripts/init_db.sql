-- MySQL 初始化脚本

CREATE DATABASE IF NOT EXISTS gameserver;
USE gameserver;

-- 玩家表
CREATE TABLE IF NOT EXISTS players (
  player_id BIGINT PRIMARY KEY,
  account VARCHAR(64) NOT NULL UNIQUE,
  password VARCHAR(256) NOT NULL,
  salt VARCHAR(64),
  name VARCHAR(64) NOT NULL,
  level INT DEFAULT 1,
  exp BIGINT DEFAULT 0,
  gold BIGINT DEFAULT 0,
  server_id INT DEFAULT 1,
  score INT DEFAULT 0,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  INDEX idx_account (account),
  INDEX idx_score (score)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 对局记录表
CREATE TABLE IF NOT EXISTS battles (
  battle_id BIGINT PRIMARY KEY,
  room_id BIGINT,
  player1_id BIGINT,
  player2_id BIGINT,
  winner_id BIGINT,
  loser_id BIGINT,
  duration_s INT,
  reason VARCHAR(64),
  mode INT DEFAULT 1,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  INDEX idx_player (player1_id),
  INDEX idx_player2 (player2_id),
  INDEX idx_winner (winner_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 排行榜表
CREATE TABLE IF NOT EXISTS rankings (
  player_id BIGINT PRIMARY KEY,
  player_name VARCHAR(64),
  score INT DEFAULT 0,
  rank INT DEFAULT 0,
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  INDEX idx_score (score DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;