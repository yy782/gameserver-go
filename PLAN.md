# Go 游戏服务器 - 项目规划文档

## 项目概述

将原 C++ 游戏服务器项目完整翻译为 Go 版本。保留所有功能和架构特性，使用 Go 最佳实践实现。

## 已完成部分

### 1. 项目结构 ✓
- 完整的目录结构
- go.mod 依赖声明
- protobuf 定义（common.proto, protocol.proto, cluster.proto）

### 2. 基础设施 ✓
- 配置系统（JSON 格式）
- 日志系统
- 工具函数（哈希、时间、距离计算）
- 网络协议编解码（frame format）
- Redis 客户端包装
- MySQL 客户端包装

### 3. 各微服务核心实现 ✓
- **Center**: 服务注册/发现、token 管理
- **Login**: 账号注册/认证、token 签发
- **Game**: 房间管理、匹配系统、帧同步/状态同步模拟、Tick 循环
- **Gateway**: 会话管理、消息路由、推送
- **Rank**: 排行榜管理

### 4. gRPC 服务定义 ✓
- 所有 5 个 gRPC 服务完整定义
- 消息类型对齐 C++ 版本

### 5. 脚本和文档 ✓
- README.md - 项目文档
- build.sh - 构建脚本
- start_all.sh - 启动脚本
- scripts/init_db.sql - 数据库初始化

## 待完成部分

### 1. gRPC 客户端连接
- [ ] Game 服务需要连接到 Gateway 进行推送
- [ ] Gateway 需要连接到 Login 服务进行身份验证
- [ ] Gateway 需要连接到 Game 服务进行匹配
- [ ] Gateway 需要连接到 Rank 服务获取排行榜

### 2. 服务发现完善
- [ ] Center 服务的完整实现（目前只有框架）
- [ ] 各服务启动时向 Center 注册
- [ ] 各服务定期心跳保活
- [ ] 动态获取服务地址

### 3. 网络层完善
- [ ] Gateway TCP 连接的完整处理
- [ ] 心跳检测机制
- [ ] 断线重连支持
- [ ] 顶号踢线逻辑

### 4. 游戏逻辑完善
- [ ] Room 的完整 Tick 循环与推送
- [ ] 帧同步和状态同步的完整实现
- [ ] 对局超时判定
- [ ] 断线判负

### 5. 数据库集成
- [ ] Player 结构体与 protobuf 的完整映射
- [ ] 玩家信息的持久化
- [ ] 对局记录的保存
- [ ] 排行榜数据的同步

### 6. Redis 集成
- [ ] Token 存储和验证
- [ ] 匹配池的管理
- [ ] 排行榜的 ZSET 操作
- [ ] 会话数据缓存

### 7. 错误处理和日志
- [ ] 更详细的日志记录
- [ ] 错误情况的优雅处理
- [ ] 监控指标

### 8. 测试
- [ ] 单元测试
- [ ] 集成测试
- [ ] 与 Python 客户端的兼容性测试

## 依赖需求

### 系统依赖
- Go 1.22+
- protoc（protobuf 编译器）
- MySQL 8.0+
- Redis 6.0+

### Go 依赖
```
google.golang.org/grpc v1.60.0
google.golang.org/protobuf v1.32.0
github.com/go-redis/redis/v8 v8.11.5
gorm.io/gorm v1.25.5
gorm.io/driver/mysql v1.5.4
```

## 编译步骤

1. 确保已安装 protoc 和 Go 工具链
2. 生成 protobuf 代码
3. go mod tidy
4. go build 各个服务

## 与 C++ 版本对应关系

| C++ 文件 | Go 位置 | 状态 |
|---------|--------|------|
| src/common/ | internal/common/ | ✓ 完成 |
| src/net/protocol.* | internal/net/protocol.go | ✓ 完成 |
| src/rpc/redis_client.* | internal/rpc/redis_client.go | ✓ 完成 |
| src/rpc/mysql_client.* | internal/rpc/mysql_client.go | ✓ 框架 |
| src/center/main.cpp | cmd/center/ | ✓ 框架 |
| src/login/main.cpp | cmd/login/ | ✓ 框架 |
| src/gateway/ | cmd/gateway/, internal/gateway/ | ○ 部分 |
| src/game/ | cmd/game/, internal/game/ | ○ 部分 |
| src/rank/main.cpp | cmd/rank/ | ✓ 框架 |

## 下一步

1. 完成所有 gRPC 客户端连接
2. 实现服务间通信
3. 完善网络层（TCP 连接管理）
4. 测试与 Python 客户端的兼容性