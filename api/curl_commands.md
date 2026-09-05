# api（运营管理 HTTP 面）curl 使用文档

api 进程是独立的运营/管理 HTTP 服务（Gin，默认监听 `9700`），对外提供 HTTP + JSON，
内部通过 gRPC/Redis/MySQL 聚合数据。所有验证用标准的 HTTP 客户端（curl / Postman / 浏览器）即可，
**不需要**项目内部的自定义 TCP 客户端。

> 本文档所有命令默认在项目根目录执行：`cd /home/yy/programs/Go/gameserver`
> 默认地址：`http://127.0.0.1:9700`（以 `config/api.json` 实际配置为准）

---

## 0. 前提：依赖与进程

api 进程本身只做「翻译 + 聚合」，数据来自下游，验证前请确认整条链路是活的：

| 依赖 | 地址 | 挂了会怎样 |
|---|---|---|
| center | 127.0.0.1:9100 | `/healthz` 的 `center=false`，`/servers` 返回 502 |
| Redis | 127.0.0.1:6379 | `/healthz` 的 `redis=false`，`/rank/top` 空或 503 |
| MySQL | 127.0.0.1:3306 (game_db) | `/players/:id` 无法查到档案 |

集群一键启动：

```bash
cd /home/yy/programs/Go/gameserver
bash start_all.sh          # center/login/gateway/game×2/rank/api 全部拉起
```

确认 api 进程已启动：

```bash
ps aux | grep 'bin/api' | grep -v grep
# 无输出说明没起，单独补：
./bin/api -config config/api.json &
```

---

## 1. 接口总览

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| GET | `/healthz` | 无需 | 进程存活 + Redis/center 依赖探活 |
| POST | `/api/v1/admin/login` | 无需 | 运营账号登录，签发 JWT |
| GET | `/api/v1/me` | Bearer | 当前登录运营账号信息 |
| GET | `/api/v1/servers` | Bearer | 实时服务拓扑（来自 center 注册表） |
| GET | `/api/v1/rank/top` | Bearer | 排行榜 TopN，参数 `n`(1~100，默认 10) |
| GET | `/api/v1/players/:id` | Bearer | 玩家档案（来自 MySQL 玩家表） |

统一响应体：`{"code":0,"msg":"ok","data":{...}}`，`code=0` 表示成功；非 0 时与 HTTP
状态码语义对齐（如 401 未登录 / 404 不存在 / 502 中心服不可用）。

默认运营账号（见 `config/api.json`）：账号 `admin`，密码 `admin123`，token 有效期 7200s。

---

## 2. 基础探活（无需登录）

```bash
curl -s http://127.0.0.1:9700/healthz
```

期望输出（`redis` 与 `center` 均为 `true` 才算健康）：

```json
{"code":0,"msg":"ok","data":{"service":"api","uptime_sec":123,"redis":true,"center":true}}
```

任何依赖为 `false` 或返回 503，先检查对应的 center/Redis 是否存活。

---

## 3. 登录并保存 token

```bash
# 有 jq 的机器：
TOKEN=$(curl -s -X POST http://127.0.0.1:9700/api/v1/admin/login \
  -H 'Content-Type: application/json' \
  -d '{"account":"admin","password":"admin123"}' | jq -r .data.token)

# 没有 jq 时的等价写法：
# TOKEN=$(curl -s -X POST http://127.0.0.1:9700/api/v1/admin/login \
#   -H 'Content-Type: application/json' \
#   -d '{"account":"admin","password":"admin123"}' \
#   | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["token"])')
```

验证 token 拿到（应为一段 `xxx.yyy.zzz` 三段式 JWT）：

```bash
echo "$TOKEN"
```

也可以先看一次登录完整返回，确认字段齐全：

```bash
curl -s -X POST http://127.0.0.1:9700/api/v1/admin/login \
  -H 'Content-Type: application/json' \
  -d '{"account":"admin","password":"admin123"}'
```

期望输出：

```json
{"code":0,"msg":"ok","data":{"account":"admin","token":"eyJhbGciOiJIUzI1NiIs...","token_type":"Bearer","expire_sec":7200}}
```

> 密码错误应返回 `401 {"code":401,"msg":"账号或密码错误"}`。

---

## 4. 受保护接口（需带 `Authorization: Bearer <token>`）

以下命令先执行第 3 步拿到 `$TOKEN`。

### 4.1 服务拓扑

```bash
curl -s http://127.0.0.1:9700/api/v1/servers \
  -H "Authorization: Bearer $TOKEN"
```

期望：按 kind 分组列出各实例及端口（gateway:9300、game:9400/9401、rank:9600、api:9700 等）。

### 4.2 当前登录账号

```bash
curl -s http://127.0.0.1:9700/api/v1/me \
  -H "Authorization: Bearer $TOKEN"
```

期望：`{"code":0,...,"data":{"account":"admin","role":"admin"}}`

### 4.3 排行榜 TopN

```bash
# 默认取前 10
curl -s http://127.0.0.1:9700/api/v1/rank/top \
  -H "Authorization: Bearer $TOKEN"

# 指定取前 20（n 合法范围 1~100，越界会被夹取到边界）
curl -s "http://127.0.0.1:9700/api/v1/rank/top?n=20" \
  -H "Authorization: Bearer $TOKEN"
```

期望：`data.list` 按名次升序返回玩家（player_id/name/level/score）。榜单为空说明还没有对局
结算数据，先跑第 6 步的演示客户端造数据。

### 4.4 玩家档案

```bash
# :id 替换为真实玩家 ID（可从排行榜返回的 player_id 拿到）
curl -s http://127.0.0.1:9700/api/v1/players/1 \
  -H "Authorization: Bearer $TOKEN"
```

期望：返回该玩家的 account/name/level/gold/score 等档案字段。
玩家不存在时返回 `404 {"code":404,"msg":"玩家不存在"}`。

---

## 5. 鉴权中间件反向用例（证明 JWT 真的在拦）

```bash
# 不带 token → 期望 401
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:9700/api/v1/servers

# 伪造 token → 期望 401
curl -s -o /dev/null -w '%{http_code}\n' \
  http://127.0.0.1:9700/api/v1/servers -H 'Authorization: Bearer abc.def.ghi'
```

两条都输出 `401` 说明 `requireAdmin` 中间件生效。

---

## 6. 造真实数据后再次验证

排行榜分数在 Redis `rank:global`，玩家档案在 MySQL——需要先跑一局游戏才有数据：

```bash
python3 scripts/client_demo.py --bots=2
```

该脚本自动完成：注册建档 → 匹配 → 对打 → 结算（分数写入排行榜）。跑完后：
- 重查 4.3，`/rank/top` 应能看到对局玩家及分数；
- 记下榜单里的 `player_id`，重查 4.4 验证 MySQL 档案。

---

## 7. 请求日志核对

```bash
tail -f logs/api.log
```

每次 HTTP 请求会打印一行访问日志：
`[api] <来源IP> <状态码> <METHOD> <URI> <耗时>`，可用于对照上面各请求是否被正确处理。

---

## 8. 常见现象速查

| 现象 | 含义 | 处理 |
|---|---|---|
| `/healthz` 返回 503 或 `redis:false` | Redis 不通 | 检查 Redis 是否启动 |
| `/healthz` 返回 `center:false` | center 不通 | 检查 center 进程（9100） |
| `/servers` 返回 502 | api → center 的 gRPC 链路断 | 确认 center 存活 |
| `/rank/top` 空列表 | Redis 里还没有排行数据 | 跑 `client_demo.py --bots=2` |
| `/players/:id` 404 | MySQL 无该玩家 | 换真实玩家 ID 或先注册建档 |
| 受保护接口 401 | token 缺失 / 无效 / 过期 | 重新执行第 3 步登录 |
| 登录报 400 参数错误 | JSON body 格式不对 | 检查 `{"account":...,"password":...}` 引号是否完整 |
