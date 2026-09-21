// 门户地址与"回门户"这一个动作。
//
// 为什么地址要走构建期变量而不是从后端拿: 应用侧**没有**一条"告诉我门户在哪"的接口 ——
// 那属于部署拓扑, 不属于身份协议(接入规范里平台只承诺"未登录时把人送回门户", 而那个
// 跳转在服务端做; 前端这一份是给"会话在浏览器里已经失效、但页面还开着"用的)。
//
// 缺省值刻意留空而不是给一个猜的域名: 猜错的表现是"点退出之后跳到一个不存在的地方",
// 而那个现象很难归因到一条环境变量上。空值时的兜底见 goToPortal。
const PORTAL_URL = (import.meta.env.VITE_ONELINK_PORTAL_URL || '').trim()

/**
 * 回门户。
 *
 * 没有配置门户地址时**只提示, 不跳**: 跳到一个空字符串会留在当前页(浏览器把空 URL 当
 * 当前地址), 于是用户看到的是"点了退出什么也没发生" —— 而那比一句明确的提示更难查。
 */
export function goToPortal() {
  if (!PORTAL_URL) {
    // 用 alert 而不是 ElMessage: 这一条发生在路由守卫里, 而守卫可能在任何组件挂载之前
    // 就跑完了 —— 那时 ElMessage 的挂载点还不存在, 消息会静默丢掉。
    window.alert(
      '会话已失效，请回到 OneLink 门户重新进入躬行。\n' +
        '（本应用未配置门户地址 VITE_ONELINK_PORTAL_URL，无法自动跳转）',
    )
    return
  }
  // 带上 next: 门户认这个参数并把人送回这里。用当前地址而不是固定首页, 是因为
  // "会话在某个页面中途失效"是最常见的一种 —— 回到那一页比回到首页少一步。
  const next = encodeURIComponent(window.location.origin + window.location.pathname)
  window.location.href = `${PORTAL_URL}${PORTAL_URL.includes('?') ? '&' : '?'}next=${next}`
}

/** 是否配了门户地址。给需要按情况改文案的地方用(例如退出登录的确认框)。 */
export function portalConfigured() {
  return !!PORTAL_URL
}
