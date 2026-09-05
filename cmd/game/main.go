package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"gameserver/api/pb"
	"gameserver/internal/common"
	"gameserver/internal/game"
	"gameserver/internal/rpc"
	"google.golang.org/grpc"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	configPath := flag.String("config", "../../../config/game.json", "config file path")
	flag.Parse()

	cfg := common.NewConfig()
	if err := cfg.LoadFile(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	name := cfg.GetString("name", "game-1")
	listenIP := cfg.GetString("listen_ip", "0.0.0.0")
	grpcPort := int(cfg.GetInt("grpc_port", 9400))
	advertiseIP := cfg.GetString("advertise_ip", "127.0.0.1")
	redisHost := cfg.GetString("redis_host", "127.0.0.1")
	redisPort := int(cfg.GetInt("redis_port", 6379))
	centerHost := cfg.GetString("center_host", "127.0.0.1")
	centerPort := int(cfg.GetInt("center_port", 9100))

	selfAddr := common.FormatAddr(advertiseIP, grpcPort)

	common.Info("Starting %s on %s:%d (advertise %s), redis=%s:%d center=%s:%d",
		name, listenIP, grpcPort, selfAddr, redisHost, redisPort, centerHost, centerPort)

	// Redis 客户端（匹配池 / 房间路由）
	redisClient := rpc.NewRedisClient(redisHost, redisPort)

	// 集群客户端（注册中心 / 排行上报）
	clusterClient := rpc.NewClusterClient()
	clusterClient.SetCenterAddr(rpc.ServiceAddr{Host: centerHost, Port: int32(centerPort)})

	// 网关推送客户端（快照 / 帧 / 结果）
	pushClient := rpc.NewGatewayPushClient()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 创建游戏服务并启动后台任务（房间主循环 / 匹配线程）
	gameSvc := game.NewGameService(redisClient, clusterClient, pushClient, selfAddr, name)
	gameSvc.InitRoomSeq(ctx)
	gameSvc.Start(ctx)

	// 注册到中心服（失败重试 10 次，共约 5s）+ 5s 心跳 + 刷新排行榜服务地址
	go func() {
		registered := false
		for i := 0; i < 10; i++ {
			if err := clusterClient.RegisterService(ctx, name, listenIP, grpcPort, "game"); err == nil {
				registered = true
				break
			}
			common.Warn("中心服注册失败（第 %d 次），500ms 后重试", i+1)
			time.Sleep(500 * time.Millisecond)
		}
		if !registered {
			common.Fatal("中心服注册失败（重试 10 次仍失败）")
		}
		hb := time.NewTicker(5 * time.Second)
		defer hb.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-hb.C:
				if err := clusterClient.Heartbeat(ctx, name); err != nil {
					common.Warn("[game] 中心服心跳失败，尝试重新注册")
					_ = clusterClient.RegisterService(ctx, name, listenIP, grpcPort, "game")
				}
				// 刷新排行榜服务地址（按 kind 轮询中心服）
				if services, err := clusterClient.GetServiceList(ctx); err == nil {
					for _, e := range services {
						if e.Kind == "rank" {
							clusterClient.SetRankAddr(rpc.ServiceAddr{Host: e.Host, Port: e.Port})
						}
					}
				}
			}
		}
	}()

	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", listenIP, grpcPort))
	if err != nil {
		common.Fatal("Failed to listen: %v", err)
	}
	defer lis.Close()

	server := grpc.NewServer()
	pb.RegisterGameServiceServer(server, gameSvc)

	// 监听退出信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		common.Info("Shutting down %s", name)
		gameSvc.Stop()
		server.GracefulStop()
		os.Exit(0)
	}()

	// 读取输入防止退出（stdin 关闭即退出该 goroutine，避免后台空转）
	go func() {
		reader := bufio.NewReader(os.Stdin)
		for {
			if _, err := reader.ReadString('\n'); err != nil {
				return
			}
		}
	}()

	common.Info("%s started", name)
	if err := server.Serve(lis); err != nil {
		common.Fatal("Server error: %v", err)
	}
}
