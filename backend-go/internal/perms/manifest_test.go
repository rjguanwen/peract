package perms

// 清单的两个载体必须逐条相等。
//
// 这条用例存在的理由只有一条, 而它挡的是一类**静默**故障: deploy/perms.manifest.json
// 是给人改的那一份(它同时是 seed-perms.sql 的来源与 CI 上报的输入), 而 manifest.go 是
// 运行期应答平台拉取的那一份。只改前者, 平台上由上报路径写进去的权限点是新的, 而平台
// 在管理台上点"拉取权限点"取到的是旧的 —— 两条路径给出两份不同的清单, 且两边各自的
// 日志都正常。只改后者则相反。
//
// 比对的是**语义**而不是字节: 两边都先解成同一个结构体再各自序列化, 于是文件里的缩进、
// 空行、字段顺序都不会影响结论, 而任何一个字段的值不同都会被抓到。

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	onelinksdk "github.com/onelink/platform/sdk/go/onelink"
)

// manifestFile 部署产物那一份。相对本包目录: backend-go/internal/perms -> 仓库根的 deploy/。
const manifestFile = "../../../deploy/perms.manifest.json"

func TestManifestMatchesDeployFile(t *testing.T) {
	raw, err := os.ReadFile(filepath.FromSlash(manifestFile))
	if err != nil {
		t.Fatalf("读清单文件失败(它必须与代码一起进版本库): %v", err)
	}
	var file []onelinksdk.PermDecl
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("解析 %s: %v", manifestFile, err)
	}
	if len(file) == 0 {
		t.Fatal("清单文件里一条声明都没有")
	}

	if len(Manifest) != len(file) {
		t.Fatalf("条数不一致: 代码里 %d 条, %s 里 %d 条 —— 两边都要改",
			len(Manifest), manifestFile, len(file))
	}

	for i := range file {
		// 逐条比而不是整体比: 整体比只会给出一句"两份 JSON 不相等", 而这份清单有 15 条,
		// 读的人得自己去 diff。逐条比能直接说出是第几条、哪个字段。
		code, err := json.Marshal(Manifest[i])
		if err != nil {
			t.Fatalf("序列化代码里的第 %d 条失败: %v", i, err)
		}
		want, err := json.Marshal(file[i])
		if err != nil {
			t.Fatalf("序列化文件里的第 %d 条失败: %v", i, err)
		}
		if bytes.Equal(code, want) {
			continue
		}
		t.Errorf("第 %d 条(%s)不一致:\n  代码: %s\n  文件: %s",
			i, file[i].Key, code, want)
	}
}

// TestManifestKeysAreNamespaced 每一条的 key 都必须挂在应用编码下。
//
// 平台侧会拒(保留前缀 + 命名空间校验), 但那条拒绝发生在**部署或拉取的时候**, 而它的
// 现场是"某个权限点一直是空的"或者"平台说清单不合法"。这条判据在这里只是一行字符串比较,
// 而它挡掉的是"改清单时手滑写成了 platform:xxx" —— 那种错在平台侧会连带让整批清单被拒。
func TestManifestKeysAreNamespaced(t *testing.T) {
	const appCode = "task-system:"
	seen := map[string]bool{}
	for _, d := range Manifest {
		if !bytes.HasPrefix([]byte(d.Key), []byte(appCode)) {
			t.Errorf("%s 没有以 %s 开头", d.Key, appCode)
		}
		if seen[d.Key] {
			t.Errorf("%s 在清单里出现了两次", d.Key)
		}
		seen[d.Key] = true
		if d.Name == "" || d.Type == "" {
			t.Errorf("%s 缺少 name 或 type", d.Key)
		}
	}
}

// TestManifestParentsExist 父级必须也在清单里。
//
// 平台落库时要求父级先存在(它按顺序逐条写), 而"父级拼错一个字母"的现场是一句
// "父级不存在"的整批拒绝 —— 那时人只能自己拿清单逐条对。这里比一遍更早也更快。
func TestManifestParentsExist(t *testing.T) {
	byKey := map[string]bool{}
	for _, d := range Manifest {
		byKey[d.Key] = true
	}
	for _, d := range Manifest {
		if d.Parent != "" && !byKey[d.Parent] {
			t.Errorf("%s 的父级 %s 不在清单里", d.Key, d.Parent)
		}
	}
}
