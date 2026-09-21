package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	onelinksdk "github.com/onelink/platform/sdk/go/onelink"

	"taskbackend/internal/config"
	"taskbackend/internal/database"
	"taskbackend/internal/handler"
	"taskbackend/internal/middleware"
	"taskbackend/internal/onelink"
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
	// 这里曾经有 InitAdmin: 库里没有管理员就建一个并给默认口令。它删掉了, 因为
	// 账号归 OneLink 管 —— 应用侧自动建管理员等于在平台的账号体系之外留一个后门账号。

	notifier := service.NewNotifier(cfg)
	notifier.Start()

	// 会话与档案先建, 它们要交给 handler 挂路由。没配 OneLink 时两者为 nil,
	// handler 的 RegisterRoutes 会整族跳过(见那里的注释) —— 那种状态只该出现在本地开发。
	guard, profiles, sessionStore, err := buildOnelink(cfg, db)
	if err != nil {
		return err
	}

	h := handler.New(db, cfg, guard, profiles, notifier)
	sched := scheduler.New(db, cfg, notifier)
	sched.Start()

	// 先停调度器再停通知队列：否则调度器可能在队列关闭后继续入队，
	// 投递协程已退出，通知会静默丢失。
	defer func() {
		sched.Stop()
		notifier.Stop(5 * time.Second)
	}()
	defer h.Close()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())
	r.Use(middleware.CORS(cfg))

	// 不显式配置受信代理时，gin 会无条件采信 X-Forwarded-For，
	// 导致来源 IP 可被伪造头随意绕过。
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
			log.Printf("HTTP 服务退出: %v", err)
			errCh <- err
		}
	}()

	// 过期会话的周期清理。数据库里的行不会像 SDK 的 MemoryStore 那样自己消失, 而每一行
	// 都带着两个令牌原文 —— 不清的话这张表会慢慢变成"一个躺着几百张已过期凭据的库"。
	// 它挂在调度器之外单独起一个 ticker: 会话清理与业务提醒是两件事, 混进 scheduler 会让
	// 那个包的测试多出一个与任务无关的计时器。
	purgeStop := make(chan struct{})
	if sessionStore != nil {
		go purgeLoop(sessionStore, purgeStop)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-errCh:
		close(purgeStop)
		return err
	case <-ctx.Done():
	}

	log.Println("收到退出信号，正在优雅停机…")
	close(purgeStop)
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

// buildOnelink 装配 OneLink 接入层, 返回守卫、本地档案与会话存储。
//
// 没配齐时回 (nil, nil, nil, nil) 而不是报错: 本地开发(只想跑业务接口、或平台还没搭起来)
// 不该因为缺一个环境变量而起不来。生产环境下 config.validate 已经把"没配"变成启动失败,
// 所以这个分支只会在开发环境走到。
//
// 三个部件的分工(都在 internal/onelink 里):
//   - Store 把会话放进躬行自己的 SQLite —— 用 SDK 的 MemoryStore 的代价是"发布一次
//     全体被踢回门户";
//   - Profiles 在每次通过守卫的请求上把平台身份落成本地 user.id, 业务表外键靠它;
//   - Guard 是 SDK 的守卫, 它负责落地票据、续期、以及轮询会话存活(单点登出的滞后由
//     AliveInterval 决定)。
func buildOnelink(cfg *config.Config, db *gorm.DB) (*onelinksdk.Guard, *onelink.Profiles, *onelink.Store, error) {
	if !cfg.OnelinkConfigured() {
		return nil, nil, nil, nil
	}

	client, err := onelinksdk.New(onelinksdk.Config{
		BaseURL:   cfg.OnelinkBaseURL,
		AppCode:   cfg.OnelinkAppCode,
		AppSecret: cfg.OnelinkAppSecret,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("初始化 OneLink 客户端: %w", err)
	}

	store := onelink.NewStore(db)
	if err := store.Migrate(); err != nil {
		return nil, nil, nil, fmt.Errorf("建立会话表: %w", err)
	}

	// 离线验签: 每个请求在本地验一次 RS256 + 受众 + 过期。它挡的是"伪造一个令牌骗过
	// 本应用" —— 这一点从 Store 里查不出来(本应用读的是自己存的那份记录, 从来没验过
	// 平台签过名)。平台公钥取不到时 SDK 只记日志并按令牌寿命放行, 把"不知道"当成
	// "是伪造"会把一次平台抖动放大成登录循环。
	verifier, err := onelinksdk.NewVerifier("onelink", cfg.OnelinkAppCode, client)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("初始化 OneLink 离线验签: %w", err)
	}

	guard, err := onelinksdk.NewGuard(onelinksdk.GuardConfig{
		Client:    client,
		PortalURL: cfg.OnelinkPortalURL,
		Store:     store,
		Verifier:  verifier,
		// 单点登出的可感知滞后就是这个周期。调小它比"在关键操作前额外轮询一次"正确 ——
		// 后者会让应用代码里出现两个关于"这个人还在不在线"的真值来源。
		AliveInterval: cfg.OnelinkAliveInterval,
		Logger:        log.New(os.Stderr, "[onelink] ", log.LstdFlags),
		// 未登录一律回 401 JSON 而**不是** 302 到门户: 这个进程只服务 API, 页面由前端
		// 自己托管(见 README 的部署说明)。给机器客户端回 302 是最难自查的一类错 ——
		// fetch 会跟着跳转拿到一个 HTML 登录页, 然后把它当 JSON 解析。
		//
		// 前端据此判断"该回门户了": 它拿到的 401 是**登录态**失效, 与业务权限不足(403)
		// 是两件事, 混在一起会让一个没权限的人被反复送回门户。
		OnUnauthenticated: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"detail":"登录已失效，请从门户重新进入躬行"}`))
		}),
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("初始化 OneLink 守卫: %w", err)
	}

	// 启动时清一遍彻底过期的会话行, 之后由 purgeLoop 周期清。
	if n, err := store.PurgeExpired(context.Background(), time.Now()); err != nil {
		log.Printf("[warn] 清理过期会话失败(不影响启动): %v", err)
	} else if n > 0 {
		log.Printf("已清理 %d 条过期会话", n)
	}

	log.Printf("OneLink 接入已挂载: 应用 %s, 平台 %s, 存活轮询 %s",
		cfg.OnelinkAppCode, cfg.OnelinkBaseURL, cfg.OnelinkAliveInterval)
	return guard, onelink.NewProfiles(db), store, nil
}

// purgeInterval 会话清理周期。一小时是随手定的一个"比刷新令牌寿命短得多、又不会
// 让这条日志变成噪声"的值: 清理的收益是"库里不堆死行", 它对时效没有要求。
const purgeInterval = time.Hour

func purgeLoop(store *onelink.Store, stop <-chan struct{}) {
	ticker := time.NewTicker(purgeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-ticker.C:
			if n, err := store.PurgeExpired(context.Background(), now); err != nil {
				log.Printf("[warn] 清理过期会话失败: %v", err)
			} else if n > 0 {
				log.Printf("已清理 %d 条过期会话", n)
			}
		}
	}
}
