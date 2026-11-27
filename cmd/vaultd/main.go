// vaultd 是 Secret Gate 的服务进程：SQLite + 单二进制，托管 CI 密钥的后台。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lyzz0612/deploy-center/internal/app"
	"github.com/lyzz0612/deploy-center/internal/httpapi"
	"github.com/lyzz0612/deploy-center/internal/infra/crypto"
	"github.com/lyzz0612/deploy-center/internal/infra/sqlite"
	"github.com/lyzz0612/deploy-center/web"
)

// version 由构建注入：-ldflags "-X main.version=..."
var version = "dev"

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("vaultd: %v", err)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("vaultd", flag.ContinueOnError)
	addr := fs.String("addr", envOr("ADDR", "127.0.0.1:8788"), "HTTP 监听地址")
	dbPath := fs.String("db", "data/app.db", "SQLite 数据文件路径")
	hc := fs.Bool("healthcheck", false, "对 -addr 执行健康检查后退出（供容器 healthcheck 使用）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *hc {
		return healthcheck(*addr)
	}

	masterKey := os.Getenv("MASTER_KEY")
	if len(masterKey) < 32 {
		return fmt.Errorf("MASTER_KEY 缺失或不足 32 字节（当前 %d 字节），拒绝启动", len(masterKey))
	}
	adminPassword := os.Getenv("ADMIN_PASSWORD")
	if adminPassword == "" {
		return errors.New("ADMIN_PASSWORD 缺失，拒绝启动")
	}

	if dir := filepath.Dir(*dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建数据目录 %s: %w", dir, err)
		}
	}

	repo, err := sqlite.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("打开数据库: %w", err)
	}
	defer repo.Close()

	if err := repo.Migrate(); err != nil {
		return fmt.Errorf("迁移数据库: %w", err)
	}

	sealer, err := crypto.New([]byte(masterKey))
	if err != nil {
		return err
	}
	// 启动自检：MASTER_KEY 与库对不上直接拒绝启动
	if err := app.EnsureSentinel(repo, sealer); err != nil {
		return err
	}

	h := httpapi.New(httpapi.Deps{
		Version:       version,
		SchemaVersion: sqlite.SchemaVersion,
		Login:         app.NewLoginService(adminPassword),
		Secrets:       app.NewSecretService(repo, sealer),
		Static:        web.Dist,
	})

	srv := &http.Server{
		Addr:              *addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("vaultd %s 启动: http://%s  db=%s  后台: http://%s/admin/", version, *addr, *dbPath, *addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Println("收到退出信号，正在关闭…")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("优雅关闭: %w", err)
		}
	}
	return nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func healthcheck(addr string) error {
	if host, _, err := net.SplitHostPort(addr); err == nil && host == "0.0.0.0" {
		addr = "127.0.0.1" + addr[len("0.0.0.0"):]
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		return fmt.Errorf("健康检查失败: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("健康检查返回 %d", resp.StatusCode)
	}
	return nil
}
