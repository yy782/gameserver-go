# Go 游戏服务器

这是将原 C++ 游戏服务器项目翻译成 Go 的完整实现。包含微服务架构、gRPC、Redis、MySQL 等完整功能。

## 项目结构

```
gameserver/
├── api/pb/               # protobuf 生成的 Go 代码
├── cmd/                  # 各服务的入口
│   ├── center/           # 服务注册/发现中心
│   ├── login/            # 登录服
│   ├── gateway/          # 网关（TCP 客户端接入）
│   ├── game/             # 游戏服（×2）
│   └── rank/             # 排行榜服
├── config/               # JSON 配置文件
├── internal/             # 核心实现
│   ├── common/           # 工具函数、日志、配置
│   ├── center/           # 中心服实现
│   ├── login/            # 登录服实现
│   ├── gateway/          # 网关实现
│   ├── game/             # 游戏服实现（房间、匹配）
│   ├── rank/             # 排行榜服实现
│   ├── net/              # 网络协议编解码
│   └── rpc/              # Redis、MySQL 客户端
├── proto/                # protobuf 定义
├── tests/                # 测试
├── go.mod
└── go.sum
```

## 服务说明

| 服务 | 端口 | 职责 |
|---|---|---|
| `center` | 9100 (gRPC) | 服务注册/发现、token 校验 |
| `login` | 9200 (gRPC) | 账号注册/认证（MySQL），签发 token |
| `gateway` | 8000 (TCP) + 9300 (gRPC) | 客户端接入、协议路由、会话管理、推送 |
| `game-1/2` | 9400/9401 (gRPC) | 房间管理、匹配、帧/状态同步模拟 |
| `rank` | 9600 (gRPC) | 排行榜（Redis ZSET） |

## 快速开始

### 1. 依赖安装

需要以下依赖（可以自己下载或用包管理器）：
- Go 1.22+
- protoc（protobuf 编译器）
- MySQL 服务
- Redis 服务

### 2. 初始化数据库

```bash
mysql -u root -p < scripts/init_db.sql
```

### 3. 生成 protobuf 代码

```bash
protoc --go_out=. --go_opt=module=gameserver \
  --go-grpc_out=. --go-grpc_opt=module=gameserver \
  -I proto proto/*.proto
```

### 4. 下载依赖

```bash
go mod tidy
```

### 5. 编译

```bash
go build -o bin/center ./cmd/center
go build -o bin/login ./cmd/login
go build -o bin/gateway ./cmd/gateway
go build -o bin/game ./cmd/game
go build -o bin/rank ./cmd/rank
```

### 6. 启动服务

每个服务在单独的终端启动：

```bash
# 终端1 - center
./bin/center -config config/center.json

# 终端2 - login
./bin/login -config config/login.json

# 终端3 - gateway
./bin/gateway -config config/gateway.json

# 终端4 - game-1
./bin/game -config config/game.json

# 终端5 - game-2
./bin/game -config config/game-2.json

# 终端6 - rank
./bin/rank -config config/rank.json
```

## 功能特性

- **微服务架构**：6 个独立进程，通过 gRPC 通信
- **双模式对战**：
  - 帧同步（mode=0）：服务器广播输入，客户端本地模拟
  - 状态同步（mode=1）：服务器广播状态快照
- **权威服务端**：胜负结算在服务器
- **跨实例匹配**：Redis 管理匹配池，支持多 game 实例
- **顶号踢线**：同账号二次登录踢下线
- **排行榜**：Redis ZSET 实时 TopN
- **持久化**：MySQL 存储玩家信息

## 配置文件

所有配置均为 JSON 格式，位于 `config/` 目录。修改服务端口、数据库地址等参数。

## 与 C++ 版本的差异

- 协议兼容：同一套 protobuf 定义，可与 Python 客户端交互
- 实现语言：从 C++20 改为 Go 1.22
- 依赖库：
  - gorm（数据库）替代 mysqlclient
  - go-redis 替代 hiredis
  - 标准库 gRPC 替代 gRPC C++
- 配置格式：保持 JSON 兼容

## 测试

使用原 C++ 项目的 Python 客户端测试：

```bash
python3 examples/auto_bot.py --mode 0 --bots 2
python3 examples/interactive_client.py
```

## TODO

- [ ] 完整的集群服务发现
- [ ] 网关推送的 gRPC 客户端连接
- [ ] 更详细的日志和监控
- [ ] 单元测试
- [ ] 压力测试脚本