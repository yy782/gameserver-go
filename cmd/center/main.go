package main

import (
	"flag"
	"fmt"
	"gameserver/internal/center"
	"gameserver/internal/common"
	"google.golang.org/grpc"
	"gameserver/api/pb"
	"net"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	configPath := flag.String("config", "../../../config/center.json", "config file path")
	flag.Parse()

	cfg := common.NewConfig()
	if err := cfg.LoadFile(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	name := cfg.GetString("name", "center")
	listenIP := cfg.GetString("listen_ip", "0.0.0.0")
	grpcPort := int(cfg.GetInt("grpc_port", 9100))
	redisHost := cfg.GetString("redis_host", "127.0.0.1")
	redisPort := int(cfg.GetInt("redis_port", 6379))

	common.Info("Starting %s on %s:%d", name, listenIP, grpcPort)

	// 创建中心服务
	centerSvc := center.NewCenter(redisHost, redisPort)

	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", listenIP, grpcPort))
	if err != nil {
		common.Fatal("监听失败: %v", err)
	}
	defer lis.Close()

	server := grpc.NewServer()
	pb.RegisterCenterServiceServer(server, centerSvc)

	// 监听退出信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		common.Info("关闭 %s", name)
		server.GracefulStop()
	}()

	common.Info("%s 已启动", name)
	if err := server.Serve(lis); err != nil {
		common.Fatal("Server error: %v", err)
	}
}