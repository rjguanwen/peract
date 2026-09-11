package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"taskbackend/internal/config"
	"taskbackend/internal/database"
	"taskbackend/internal/handler"
	"taskbackend/internal/middleware"
	"taskbackend/internal/scheduler"
	"taskbackend/internal/service"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("躬行启动失败: %v", err)
	}
}

func run() error {
	cfg := config.Load()

	db, err := database.Open(cfg)
	if err != nil {
		return err
	}
	if err := database.Migrate(db); err != nil {
		return err
	}
	if err := database.InitAdmin(db, cfg); err != nil {
		return err
	}

	notifier := service.NewNotifier(cfg)
	notifier.Start()

	auth := middleware.NewAuth(cfg, db)
	h := handler.New(db, cfg, auth, notifier)
	sched := scheduler.New(db, cfg, notifier)
	sched.Start()

	// 先停调度器再停通知队列：否则调度器可能在队列关闭后继续入队，
	// 投递协程已退出，通知会静默丢失。
	defer func() {
		sched.Stop()
		notifier.Stop(5 * time.Second)
	}()
	defer auth.Stop()
	defer h.Close()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())
	r.Use(middleware.CORS(cfg))

	// 不显式配置受信代理时，gin 会无条件采信 X-Forwarded-For，
	// 导致限流的来源 IP 可被伪造头随意绕过。
	if err := r.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		return err
	}

	h.RegisterRoutes(r)
	// 上传文件静态访问（头像等，公开）
	r.Static("/uploads", cfg.UploadDir)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// 监听顺序：先 Listen，成功后才等待退出信号，
	// 避免端口占用时白等一次 Shutdown。
	errCh := make(chan error, 1)
	go func() {
		log.Printf("%s 已启动，监听 :%s（%s）", cfg.ProjectName, cfg.Port, cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Println("收到退出信号，正在优雅停机…")
	// 通知协程已在上面通过 defer 关闭，这里只等在途 HTTP 请求收尾
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("优雅停机超时，强制退出: %v", err)
		return srv.Close()
	}
	log.Println("已安全退出")
	return nil
}
