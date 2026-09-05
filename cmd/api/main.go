package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gameserver/internal/api"
	"gameserver/internal/common"
	"gameserver/internal/rpc"

	"github.com/gin-gonic/gin"
)

func main() {
	configPath := flag.String("config", "../../../config/api.json", "config file path")
	flag.Parse()

	cfg := common.NewConfig()
	if err := cfg.LoadFile(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	name := cfg.GetString("name", "api")
	host := cfg.GetString("host", "0.0.0.0")
	httpPort := int(cfg.GetInt("http_port", 9700))
	centerHost := cfg.GetString("center_host", "127.0.0.1")
	centerPort := int(cfg.GetInt("center_port", 9100))
	redisHost := cfg.GetString("redis_host", "127.0.0.1")
	redisPort := int(cfg.GetInt("redis_port", 6379))
	mysqlHost := cfg.GetString("mysql_host", "127.0.0.1")
	mysqlPort := int(cfg.GetInt("mysql_port", 3306))
	mysqlUser := cfg.GetString("mysql_user", "root")
	mysqlPassword := cfg.GetString("mysql_password", "")
	mysqlDB := cfg.GetString("mysql_db", "game_db")

	// 运营后台账号与 JWT 配置（本地比对，不落库）
	adminCfg := api.Config{
		AdminAccount:  cfg.GetString("admin_account", "admin"),
		AdminPassword: cfg.GetString("admin_password", "admin123"),
		JWTSecret:     cfg.GetString("jwt_secret", "gameserver-admin-default-secret"),
		JWTExpireSec:  cfg.GetInt("jwt_expire_sec", 7200),
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		mysqlUser, mysqlPassword, mysqlHost, mysqlPort, mysqlDB)

	mysqlClient, err := rpc.NewMySQLClient(dsn)
	if err != nil {
		common.Fatal("Failed to init MySQL client: %v", err)
	}
	redisClient := rpc.NewRedisClient(redisHost, redisPort)

	cc := rpc.NewClusterClient()
	centerAddr := rpc.ServiceAddr{Host: centerHost, Port: int32(centerPort)}
	cc.SetCenterAddr(centerAddr)

	// 后台任务：向 center 注册（kind=api）+ 心跳 + 发现 rank 地址
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc := api.NewApiService(name, host, httpPort, redisClient, mysqlClient, cc, centerAddr)
	svc.Start(ctx)
	defer svc.Stop()

	gin.SetMode(gin.ReleaseMode)
	srv := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", host, httpPort),
		Handler: api.NewServer(svc, adminCfg).Handler(),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	common.Info("Starting %s (admin HTTP) on %s:%d", name, host, httpPort)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			common.Fatal("HTTP server error: %v", err)
		}
	case <-sigChan:
		common.Info("Shutting down %s", name)
		svc.Stop()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
		common.Info("%s stopped", name)
	}
}
