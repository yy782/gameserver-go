package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"gameserver/api/pb"
	"gameserver/internal/common"
	"gameserver/internal/rank"
	"gameserver/internal/rpc"
	"google.golang.org/grpc"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	configPath := flag.String("config", "../../../config/rank.json", "config file path")
	flag.Parse()

	cfg := common.NewConfig()
	if err := cfg.LoadFile(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	name := cfg.GetString("name", "rank")
	host := cfg.GetString("host", "0.0.0.0")
	listenIP := cfg.GetString("listen_ip", "0.0.0.0")
	grpcPort := int(cfg.GetInt("grpc_port", 9600))
	redisHost := cfg.GetString("redis_host", "127.0.0.1")
	redisPort := int(cfg.GetInt("redis_port", 6379))
	centerHost := cfg.GetString("center_host", "127.0.0.1")
	centerPort := int(cfg.GetInt("center_port", 9100))

	common.Info("Starting %s on %s:%d", name, listenIP, grpcPort)

	redisClient := rpc.NewRedisClient(redisHost, redisPort)
	rankSvc := rank.NewRankService(redisClient)

	// 注册到中心服并定期心跳
	cc := rpc.NewClusterClient()
	cc.SetCenterAddr(rpc.ServiceAddr{Host: centerHost, Port: int32(centerPort)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		hb := time.NewTicker(5 * time.Second)
		defer hb.Stop()
		reg := time.NewTicker(10 * time.Second)
		defer reg.Stop()
		for {
			select {
			case <-hb.C:
				_ = cc.Heartbeat(ctx, name)
			case <-reg.C:
				_ = cc.RegisterService(ctx, name, host, grpcPort, "rank")
			}
		}
	}()
	if err := cc.RegisterService(ctx, name, host, grpcPort, "rank"); err != nil {
		common.Warn("rank 注册到中心服失败: %v", err)
	}

	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", listenIP, grpcPort))
	if err != nil {
		common.Fatal("Failed to listen: %v", err)
	}
	defer lis.Close()

	server := grpc.NewServer()
	pb.RegisterRankServiceServer(server, rankSvc)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		common.Info("Shutting down %s", name)
		server.GracefulStop()
	}()

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
