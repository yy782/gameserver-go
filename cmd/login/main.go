package main

import (
	"flag"
	"fmt"
	"gameserver/api/pb"
	"gameserver/internal/common"
	"gameserver/internal/login"
	"gameserver/internal/rpc"
	"google.golang.org/grpc"
	"net"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	configPath := flag.String("config", "../../../config/login.json", "config file path")
	flag.Parse()

	cfg := common.NewConfig()
	if err := cfg.LoadFile(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	name := cfg.GetString("name", "login")
	listenIP := cfg.GetString("listen_ip", "0.0.0.0")
	grpcPort := int(cfg.GetInt("grpc_port", 9200))
	mysqlHost := cfg.GetString("mysql_host", "127.0.0.1")
	mysqlPort := int(cfg.GetInt("mysql_port", 3306))
	mysqlUser := cfg.GetString("mysql_user", "root")
	mysqlPassword := cfg.GetString("mysql_password", "")
	mysqlDB := cfg.GetString("mysql_db", "gameserver")

	// MySQL 连接字符串
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		mysqlUser, mysqlPassword, mysqlHost, mysqlPort, mysqlDB)

	// 连接 MySQL
	mysqlClient, err := rpc.NewMySQLClient(dsn)
	if err != nil {
		common.Fatal("Failed to connect MySQL: %v", err)
	}

	common.Info("Starting %s on %s:%d", name, listenIP, grpcPort)

	loginSvc := login.NewLogin(mysqlClient)

	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", listenIP, grpcPort))
	if err != nil {
		common.Fatal("Failed to listen: %v", err)
	}
	defer lis.Close()

	server := grpc.NewServer()
	pb.RegisterLoginServiceServer(server, loginSvc)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		common.Info("Shutting down %s", name)
		server.GracefulStop()
	}()

	common.Info("%s started", name)
	if err := server.Serve(lis); err != nil {
		common.Fatal("Server error: %v", err)
	}
}