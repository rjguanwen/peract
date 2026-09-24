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
	"taskbackend/internal/perms"
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
	guard, profiles, sessionStore, permPull, unread, err := buildOnelink(cfg, db)
	if err != nil {
		return err
	}

	h := handler.New(db, cfg, guard, profiles, notifier, permPull, unread)
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

// buildOnelink 装配 OneLink 接入层, 返回守卫、本地档案、会话存储与两个平台入站端点。
//
// 没配齐时回 (nil, nil, nil, nil, nil, nil) 而不是报错: 本地开发(只想跑业务接口、或平台
// 还没搭起来)不该因为缺一个环境变量而起不来。生产环境下 config.validate 已经把"没配"
// 变成启动失败, 所以这个分支只会在开发环境走到。
//
// 五个部件的分工(前三个都在 internal/onelink 里):
//   - Store 把会话放进躬行自己的 SQLite —— 用 SDK 的 MemoryStore 的代价是"发布一次
//     全体被踢回门户";
//   - Profiles 在每次通过守卫的请求上把平台身份落成本地 user.id, 业务表外键靠它;
//   - Guard 是 SDK 的守卫, 它负责落地票据、续期、以及轮询会话存活(单点登出的滞后由
//     AliveInterval 决定)。
//   - permPull 是**反方向**的入口之一: 平台在管理台上点"拉取权限点"时来取躬行的清单
//     (见 SDK 的 NewPermPullHandler)。它挂在登录守卫之外 —— 调用方是平台, 那里没有
//     会话, 而它的守卫是平台签名(用躬行自己的密钥验)。清单取 internal/perms.Manifest:
//     与 cmd/perm-sync 上报的是同一份, 于是"上报写进去的"与"拉取读出来的"不可能不同。
//   - unread 是同方向的另一个入口: 平台问"**某个人**在躬行有多少未读提醒", 用来画门户上
//     躬行那张卡片的角标(见 SDK 的 NewUnreadHandler)。它与 permPull 共用同一把密钥, 但
//     平台侧用的是另一份待签名串(多一段 userId), 所以两边不可能互相冒充。回调落到
//     handler.CountUnreadByPlatformUser —— 与躬行自己的未读数接口**同一个口径**。
func buildOnelink(cfg *config.Config, db *gorm.DB) (
	*onelinksdk.Guard, *onelink.Profiles, *onelink.Store, http.Handler, http.Handler, error) {
	if !cfg.OnelinkConfigured() {
		return nil, nil, nil, nil, nil, nil
	}

	client, err := onelinksdk.New(onelinksdk.Config{
		BaseURL:   cfg.OnelinkBaseURL,
		AppCode:   cfg.OnelinkAppCode,
		AppSecret: cfg.OnelinkAppSecret,
	})
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("初始化 OneLink 客户端: %w", err)
	}

	store := onelink.NewStore(db)
	if err := store.Migrate(); err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("建立会话表: %w", err)
	}

	// 离线验签: 每个请求在本地验一次 RS256 + 受众 + 过期。它挡的是"伪造一个令牌骗过
	// 本应用" —— 这一点从 Store 里查不出来(本应用读的是自己存的那份记录, 从来没验过
	// 平台签过名)。平台公钥取不到时 SDK 只记日志并按令牌寿命放行, 把"不知道"当成
	// "是伪造"会把一次平台抖动放大成登录循环。
	verifier, err := onelinksdk.NewVerifier("onelink", cfg.OnelinkAppCode, client)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("初始化 OneLink 离线验签: %w", err)
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
		return nil, nil, nil, nil, nil, fmt.Errorf("初始化 OneLink 守卫: %w", err)
	}

	// 权限清单端点。**清单为空时这里直接启动失败**(SDK 的判据): 一个挂出来却是空清单的
	// 端点配上平台侧的"停用清单外的行"会把躬行已有的权限点全部停用, 而它的成因几乎总是
	// "清单被清空了" —— 那件事应该在这里炸, 不是等平台来拉的时候。
	permPull, err := onelinksdk.NewPermPullHandler(onelinksdk.PullConfig{
		Client:   client,
		Manifest: perms.Manifest,
		Logger:   log.New(os.Stderr, "[onelink] ", log.LstdFlags),
		// AllowedIPs 留空: 主守卫是平台签名, 而来源名单的效果取决于躬行看到的对端地址
		// 是不是平台 —— 两者之间有反向代理时 RemoteAddr 是代理, 那时名单拦不住
		// "经由代理来的别人", 而填错它只会让一次拉取失败。真正的边界在网络层
		// (内网监听 / 网关按来源放行平台出口), 见部署说明。
	})
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("装配权限清单端点: %w", err)
	}

	// 用户态(未读数)端点。回调里只有一件业务事实: "这个平台用户在躬行有多少未读提醒",
	// 而它的口径由 handler.CountUnreadByPlatformUser 定 —— 与躬行自己的未读数接口共用同一个
	// clause, 所以门户上的角标与用户进去看到的未读数不可能是两个数。
	//
	// 认不出这个人时它返回 error(而不是 0): 那会让端点回 5xx, 平台把非 2xx 读成"取不到"
	// 并在门户上显示一个灰色占位。回 200+0 则会被读成"确实没有未读", 用户就不会点进来了。
	unread, err := onelinksdk.NewUnreadHandler(onelinksdk.UnreadConfig{
		Client: client,
		State: func(ctx context.Context, platformUserID int64) (onelinksdk.UserState, error) {
			n, err := handler.CountUnreadByPlatformUser(ctx, db, platformUserID)
			if err != nil {
				return onelinksdk.UserState{}, err
			}
			return onelinksdk.UserState{Unread: n}, nil
		},
		Logger: log.New(os.Stderr, "[onelink] ", log.LstdFlags),
		// AllowedIPs 留空, 理由同上面那条清单端点: 主守卫是平台签名, 而来源名单的效果
		// 取决于躬行看到的对端地址是不是平台 —— 中间有反向代理时它是代理, 填错只会让
		// 每一次未读数查询都失败(而那条失败在门户上只是一个灰色角标, 不会有人注意到)。
	})
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("装配未读数端点: %w", err)
	}

	// 启动时清一遍彻底过期的会话行, 之后由 purgeLoop 周期清。
	if n, err := store.PurgeExpired(context.Background(), time.Now()); err != nil {
		log.Printf("[warn] 清理过期会话失败(不影响启动): %v", err)
	} else if n > 0 {
		log.Printf("已清理 %d 条过期会话", n)
	}

	log.Printf("OneLink 接入已挂载: 应用 %s, 平台 %s, 存活轮询 %s",
		cfg.OnelinkAppCode, cfg.OnelinkBaseURL, cfg.OnelinkAliveInterval)
	// 这一行是给运维抄的: 管理台上那个"权限清单拉取地址"填的就是它(前面补上躬行的对外
	// 地址), 而两边不一致的表现是平台侧一句"拉取失败", 看不出是路径写错还是签名没过。
	log.Printf("权限清单端点: %s(只有平台签名能取到; 管理台的\"权限清单拉取地址\"填这个路径)",
		cfg.OnelinkPermPullPath)
	// 同样给运维抄的一行。它填错(或没填)的表现比上面那条更安静: 门户上躬行那张卡片只是
	// 不显示角标, 或者显示一个灰色问号 —— 没有一处会报错, 所以这一行日志是唯一的线索。
	log.Printf("未读数端点: %s(只有平台签名能取到; 管理台的\"未读数拉取地址\"填这个路径)",
		cfg.OnelinkUnreadPath)
	return guard, onelink.NewProfiles(db), store, permPull, unread, nil
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
