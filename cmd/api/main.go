package main

import (
	"flag"
	"fmt"
	"gameserver/internal/api"
	"gameserver/internal/common"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	configPath := flag.String("config", "../../../config/api.json", "配置文件路径")
	flag.Parse()

	cfg := common.NewConfig()
	if err := cfg.LoadFile(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	name := cfg.GetString("name", "api")
	listenIP := cfg.GetString("listen_ip", "0.0.0.0")
	httpPort := int(cfg.GetInt("http_port", 8080))

	centerAddr := cfg.GetString("center_addr", "127.0.0.1:9100")
	rankAddr := cfg.GetString("rank_addr", "127.0.0.1:9600")

	common.Info("Starting %s on %s:%d", name, listenIP, httpPort)
	common.Info("Center: %s, Rank: %s", centerAddr, rankAddr)

	apiSvc := api.NewAPIService(centerAddr, rankAddr)
	if err := apiSvc.InitClients(); err != nil {
		common.Fatal("Failed to init clients: %v", err)
	}

	go func() {
		engine := apiSvc.SetupRouter()
		addr := fmt.Sprintf("%s:%d", listenIP, httpPort)
		common.Info("HTTP server listening on %s", addr)
		if err := engine.Run(addr); err != nil {
			common.Fatal("HTTP server error: %v", err)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	common.Info("Shutting down %s", name)
	apiSvc.Close()
}