package handler

// 这个文件整块删掉了, 内容曾经是: 改口令(ChangePassword)、安全问题(GetSecurityInfo /
// SetSecurityInfo)、找回密码的四条链路(GetPasswordRecovery / SendForgotEmail /
// ResetPassword / ResetPasswordByToken)。
//
// 它们全部属于"应用自己管口令"这件事, 而接入 OneLink 之后口令归平台:
// 改密、找回、锁定、验证码都在门户里, 应用侧连用户的密码哈希都不该有(见 model.User 的注释)。
//
// 一并消失的还有这些链路各自的限流(按 IP+邮箱)与通知投递入口 —— 它们保护的接口不存在了。
// 通知投递器 service.Notifier 仍然保留, 它服务于任务提醒。
//
// 留成空文件而不是直接删除的理由见 auth.go。
