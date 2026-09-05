package main

import (
	"bufio"
	"flag"
	"fmt"
	"gameserver/api/pb"
	"gameserver/internal/common"
	"gameserver/internal/game"
	"google.golang.org/grpc"
	"net"
	"os"
	"os/signal"
	"syscall"
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
	centerHost := cfg.GetString("center_host", "127.0.0.1")
	centerPort := int(cfg.GetInt("center_port", 9100))
	redisHost := cfg.GetString("redis_host", "127.0.0.1")
	redisPort := int(cfg.GetInt("redis_port", 6379))

	common.Info("Starting %s on %s:%d", name, listenIP, grpcPort)

	// 创建游戏服务
	gameSvc := game.NewGameService()
	gameSvc.Start()

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
		server.GracefulStop()
	}()

	// 读取输入防止退出
	go func() {
		reader := bufio.NewReader(os.Stdin)
		for {
			reader.ReadString('\n')
		}
	}()

	common.Info("%s started", name)
	if err := server.Serve(lis); err != nil {
		common.Fatal("Server error: %v", err)
	}
}