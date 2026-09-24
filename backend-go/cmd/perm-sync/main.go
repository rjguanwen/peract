// Command perm-sync 把躬行的菜单与权限点清单上报给 OneLink。
//
// 它是躬行**持续集成**里的一步, 不是运行期的一部分:
//
//	go run ./cmd/perm-sync                 # 上报, 默认不接管、不删
//	go run ./cmd/perm-sync -dry-run        # 只解析并打印, 不联网
//	go run ./cmd/perm-sync -disable-missing
//	go run ./cmd/perm-sync -adopt-manual   # 一次性: 把平台侧人工登记的行交给上报维护
//
// 为什么需要它: 在 OneLink 支持应用上报之前, 一个应用的权限点只有三条登记路 ——
// 管理台逐个点、超管拿应用作用域路由批量推、或一份 seed SQL。三条都**由人执行**,
// 而人执行的代价不是"慢", 是"应用发了新版本而平台上没有对应的权限点"这件事只在
// 用户点下去、拿到 403 的时候才被发现。上报口把这一步挪进流水线, 于是"功能上线"与
// "权限点存在"变成同一次操作。
//
// 它上报的是 internal/perms.Manifest, 不是 deploy/perms.manifest.json。**清单只有
// 一个事实源, 而它是代码里那一份** —— 运行期那个 /onelink/perm-manifest 端点(平台在
// 管理台上"拉取权限点"时请求它)读的必须是同一份, 否则同一个应用会按送达路径给出两份
// 不同的清单。那份 JSON 仍然是**部署产物**(seed-perms.sql 由它生成、onelinkctl
// perm-export 校验它、给人读的也是它), 它与代码相等这件事由 perms 包的用例保证 ——
// 改一处忘了另一处时, `go test ./...` 会指名是第几条的哪个字段。
//
// 四条值得写下来:
//
//  1. **两份清单不许漂移**。这个工具与那个端点共用一份源, 所以"上报写进去了、拉取
//     读不出来"这种状态在结构上不存在。剩下唯一要防的是代码与 JSON 漂移, 那由测试挡。
//  2. **默认什么都不删**。不带 -disable-missing 时清单只增不减: 一个改名过的权限点
//     会永远留在平台上。这是有意的取舍 —— 少一次停用是可恢复的(重跑即可), 而一次
//     被截断的清单批量停用全部权限点是不可逆的收权。
//  3. **它不需要任何人的令牌**。凭据是应用自己的密钥(ONELINK_APP_SECRET), 所以
//     它可以在一个还没人登录过的新环境里跑 —— 那正是"新登记一个应用"的形态。
//  4. **它与"平台来拉"是两条并存的路, 不是新旧两代**。上报管"部署时同步", 拉取管
//     "两次部署之间核对"; 应用侧要做的就是让清单在**运行期**也拿得到(那个端点)。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	onelinksdk "github.com/onelink/platform/sdk/go/onelink"

	"taskbackend/internal/config"
	"taskbackend/internal/perms"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "权限点上报失败: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	dryRun := flag.Bool("dry-run", false, "只解析并打印, 不联网")
	adoptManual := flag.Bool("adopt-manual", false,
		"接管平台上人工登记的同名记录(一次性迁移动作; 不可逆, 接管后只能由应用改)")
	disableMissing := flag.Bool("disable-missing", false,
		"停用清单里已经没有的、本应用上报过的权限点(不删除)")
	timeout := flag.Duration("timeout", 30*time.Second, "单次请求超时")
	flag.Parse()

	// 清单来自代码(唯一事实源)。原来这里有一个 -manifest 参数与一次 os.ReadFile,
	// 删掉它的理由不是"少一个参数": 运行期那个端点读的是代码里这一份, 留着参数就等于
	// 允许"这次上报用的清单"与"平台来拉时给出去的清单"是两份不同的东西 —— 而那个状态
	// 在两边各自的日志里都看不出异常。
	decls := perms.Manifest
	if len(decls) == 0 {
		// 与 SDK 里那道判据同源, 但这里更早: 一份被清空的清单如果带着 -disable-missing
		// 跑出去, 后果是本应用上报过的权限点被**全部停用**。挡在联网之前,
		// 让那次故障停在"清单是空的"而不是"平台上的权限点都没了"。
		return errors.New("清单是空的, 拒绝提交(空清单配 -disable-missing 会停用全部上报项)")
	}

	// dry-run 在**联网之前**返回: 它的用途是"在 CI 里先看一眼这次会提交什么",
	// 而一个会顺手写数据的 dry-run 不是 dry-run。它也不需要凭据 —— 于是它在
	// 一个还没配密钥的分支上也能跑。
	if *dryRun {
		reportDryRun(decls, *adoptManual, *disableMissing)
		return nil
	}

	cfg := config.Load()
	// 只查这条链路上真正用到的三项。刻意不用 cfg.OnelinkConfigured(): 那个判据要求
	// ONELINK_PORTAL_URL, 而回门户的地址与"往平台写权限点"毫无关系 —— 拿它当闸门会让
	// 一次能成功的上报因为少配了一个前端地址而失败。
	missing := make([]string, 0, 3)
	for name, v := range map[string]string{
		"ONELINK_BASE_URL":   cfg.OnelinkBaseURL,
		"ONELINK_APP_CODE":   cfg.OnelinkAppCode,
		"ONELINK_APP_SECRET": cfg.OnelinkAppSecret,
	} {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		// 排序是为了让同一次失败在两次运行里输出一致 —— 否则 CI 日志里的 diff 会
		// 每次都在动, 而那种噪音会让人开始忽略这一段。
		sortStrings(missing)
		return fmt.Errorf("缺少配置 %s(本工具需要应用密钥, 见 .env.example)", strings.Join(missing, " / "))
	}

	client, err := onelinksdk.New(onelinksdk.Config{
		BaseURL:   cfg.OnelinkBaseURL,
		AppCode:   cfg.OnelinkAppCode,
		AppSecret: cfg.OnelinkAppSecret,
		// 比 SDK 默认的 10 秒宽一档: 一次几百条的同步是一次写事务, 而默认值是按
		// "登录链路上的三个接口"定的(见 SDK 的 Config.HTTPClient 注释)。
		UserAgent: "task-system-perm-sync/1",
	})
	if err != nil {
		return fmt.Errorf("初始化 OneLink 客户端: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	res, err := client.SyncPerms(ctx, decls, onelinksdk.PermSyncOptions{
		AdoptManual:    *adoptManual,
		DisableMissing: *disableMissing,
	})
	if err != nil {
		return err
	}
	reportResult(res)
	return nil
}

// reportDryRun 打印这次会提交什么。
//
// 按类型分组计数而不是逐条列出: 清单本身就在版本库里, 一条条打印出来只会把
// CI 日志里真正重要的那几行(计数、以及下面 reportResult 里的变更明细)淹掉。
func reportDryRun(decls []onelinksdk.PermDecl, adoptManual, disableMissing bool) {
	counts := map[string]int{}
	for _, d := range decls {
		counts[d.Type]++
	}
	fmt.Printf("dry-run: 清单 %d 条", len(decls))
	for _, t := range []string{"D", "M", "B", "A"} {
		if counts[t] > 0 {
			fmt.Printf(", %s=%d", t, counts[t])
		}
	}
	fmt.Printf("\nadoptManual=%v disableMissing=%v\n", adoptManual, disableMissing)
	fmt.Println("未联网, 未做任何改动。")
}

// reportResult 打印回执。
//
// 只列**有动作**的条目: 一次常规同步里绝大多数是 unchanged(清单与平台已经一致),
// 把几十条 unchanged 也打出来, 那几行真正变了的就会被淹没 —— 而"这次部署改动了哪些
// 权限点"正是人扫 CI 日志时唯一想看到的东西。
func reportResult(res *onelinksdk.PermSyncResult) {
	fmt.Printf("已上报到应用 %s: 新建 %d, 更新 %d, 未变 %d, 停用 %d\n",
		res.AppCode, res.Created, res.Updated, res.Unchanged, res.Disabled)
	for _, it := range res.Items {
		if it.Action == "unchanged" {
			continue
		}
		fmt.Printf("  %-10s %s\n", it.Action, it.Key)
	}
}

// sortStrings 手写插入排序而不是引入 sort: 这里最多三个元素, 而一个 import 的
// 存在理由必须比"三个元素的排序"更硬。SDK 零第三方依赖这条约束也提醒着同一件事。
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
