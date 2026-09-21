package handler

// 这个文件整块删掉了, 内容曾经是: 邀请链接的建/列/撤销(CreateInvites / ListInvites /
// RevokeInvite)、注册开关(GetSettings / SetRegistrationEnabled)以及它们的两个助手
// (markInviteUsed / invitationExpired)。
//
// 邀请是"谁来当这个应用的成员"这件事, 而成员归属现在由平台的角色授权表达:
// 在 OneLink 里给一个角色勾上躬行的权限点、再把角色授给人(sys_user_role.app_id = 躬行)。
// 应用侧再维护一份邀请名单, 就会出现"邀请过了但平台侧没授权"这种两处不一致的状态,
// 而它表现为"人被邀请进来了, 进去什么都没有"。
//
// 注册开关同理: 注册发生在门户, 应用侧开关拦不住它, 只会拦出一个假的"已关闭"。
//
// 留成空文件而不是直接删除的理由见 auth.go。
