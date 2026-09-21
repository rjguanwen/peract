module taskbackend

go 1.24

require (
	github.com/gin-gonic/gin v1.10.1
	github.com/glebarez/sqlite v1.11.0
	github.com/joho/godotenv v1.5.1
	github.com/onelink/platform/sdk/go v0.0.0
	github.com/robfig/cron/v3 v3.0.0
	gorm.io/gorm v1.31.2
)

// OneLink SDK 用本地路径替换。
//
// 它没有发布到模块代理, 而 SDK 本身**零第三方依赖**(这是它的设计约束: 它要被装进每一个
// 业务应用, 依赖树该由应用自己决定), 所以这条 replace 不会把平台的 gin/gorm/redis 拖进来。
//
// 路径是相对本文件写的: ../../../AiCoding/onelink/sdk/go。两个工程不在同一个仓库时
// 要按实际位置改, 或者干脆把 SDK 拷进本仓(它只有一个包、零依赖, 拷贝成本很低)。
replace github.com/onelink/platform/sdk/go => ../../../AiCoding/onelink/sdk/go

require (
	github.com/bytedance/sonic v1.11.6 // indirect
	github.com/bytedance/sonic/loader v0.1.1 // indirect
	github.com/cloudwego/base64x v0.1.4 // indirect
	github.com/cloudwego/iasm v0.2.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/gabriel-vasile/mimetype v1.4.3 // indirect
	github.com/gin-contrib/sse v0.1.0 // indirect
	github.com/glebarez/go-sqlite v1.21.2 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.20.0 // indirect
	github.com/goccy/go-json v0.10.2 // indirect
	github.com/google/uuid v1.3.0 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/cpuid/v2 v2.2.7 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/pelletier/go-toml/v2 v2.2.2 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.2.12 // indirect
	golang.org/x/arch v0.8.0 // indirect
	golang.org/x/crypto v0.23.0 // indirect
	golang.org/x/net v0.25.0 // indirect
	golang.org/x/sys v0.20.0 // indirect
	golang.org/x/text v0.20.0 // indirect
	google.golang.org/protobuf v1.34.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	modernc.org/libc v1.22.5 // indirect
	modernc.org/mathutil v1.5.0 // indirect
	modernc.org/memory v1.5.0 // indirect
	modernc.org/sqlite v1.23.1 // indirect
)
