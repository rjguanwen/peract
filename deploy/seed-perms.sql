-- ============================================================================
-- 躬行(task-system)在 OneLink 上的权限点与菜单登记
--
-- 这份文件与 `deploy/perms.manifest.json` 是同一份清单的两种形态:
--   manifest 给人读、给 `onelinkctl perm-export -manifest ... -app task-system` 校验,
--   这一份是实际落库的那一份。改一处必须同时改另一处 —— 清单是"应用声明了什么",
--   而库是"平台实际认了什么", 两者分叉时的表现是"代码里在判一个平台上不存在的码",
--   而那种错**不会报任何错**: RequirePerm 只是永远不放行, 看起来像权限没配。
--
-- 登记走这两条之一:
--   1) 应用作用域路由(推荐, 不用碰数据库), 归属来自**路径里的应用**:
--        POST   /api/v1/admin/apps/:id/permissions
--        PUT    /api/v1/admin/apps/:id/permissions/:itemId
--        DELETE /api/v1/admin/apps/:id/permissions/:itemId
--        GET    /api/v1/admin/apps/:id/permissions
--      它只对平台会话 + 超管开放(理由见 middleware.RequireAppScope):
--      平台侧没有任何角色"拥有"另一个应用的权限点, 于是 service 层那条
--      "不能授予自己没有的权限"在跨应用时无从判定。
--   2) 这份种子 SQL: 清单能随应用代码版本化, 且不依赖平台侧有人操作。
--
-- 为什么**不能**走平台自身那一组路由(POST /api/v1/admin/permissions):
--   那一组的归属取自**会话 appId**(见 service.Perm.Create 的 `AppID: op.AppID`,
--   请求里没有也不允许有 appId 字段)。在门户里登的是平台会话, 于是经它登记的每一行
--   都落到 app_id=1 下 —— 而躬行兑换票据时取的是"task-system 这个应用下"的权限点,
--   两边对不上时现象是"权限点在管理台看得见, 应用里的按钮却不出现",
--   且不出现的那一侧没有任何报错。
--
-- 为什么权限码必须以 `task-system:` 开头: `platform:` 是平台保留前缀, 业务应用用它
--   会被 service 层直接拒(见 service.Perm 的保留前缀校验)。而前缀用应用编码是
--   《接入规范》第 4 条的约定 —— 它让"这个码归谁"在授权树与审计日志里一眼可读。
--
-- M 与 B 的分工(一行只有一个用途, 不要混):
--   M 菜单: 填 path/component/icon, 是角色授权树上的分组节点, 也是前端
--           utils/menu.js 里每一条的 perm 判据;
--   B 按钮/接口: 填 api_method/api_uri, 是服务端 RequirePerm 的判据。
--   把两者混在一行上的后果是平台侧的 service.Perm 会按类型校验必填字段,
--   建出来的那一行在另一种用途下永远不成立。
--
-- 全部语句幂等, 重复执行安全(靠 uk_perm_key(app_id, perm_key, deleted_key) 归并)。
-- 应用还没登记时 @app_id 取到 NULL: 本库的 sql_mode 含 STRICT_TRANS_TABLES, 于是这里
--   会当场报 "Column 'app_id' cannot be null" 而不是把权限点静默挂到 0 或 1 上 ——
--   后者是一次看起来成功的错登记。
--
-- 超管不需要角色授权: 判定短路成"该应用全部启用的权限点"(见 service.Authz.PermKeysOf),
--   所以用平台超管登录门户点卡片进来时, 这些权限点直接都在。
--   非超管要进来, 需要三步(都有应用作用域路由, 且都只对超管开放):
--       POST /api/v1/admin/apps/:id/roles                     建角色(如"躬行管理员")
--       PUT  /api/v1/admin/apps/:id/roles/:itemId/permissions 勾权限点
--       PUT  /api/v1/admin/apps/:id/users/:itemId/roles       把角色授给某个人
--   最后那一条最容易漏: 少了它, 角色建好了、权限勾上了、保存也成功,
--   而那个人进去什么都没有。
-- ============================================================================

SET NAMES utf8mb4;

SET @app_id = (SELECT `id` FROM `sys_app` WHERE `app_code` = 'task-system' AND `del_flag` = 0 LIMIT 1);

-- ---------------------------------------------------------------------------
-- 一、菜单(M)。path 必须与 frontend/src/router/index.js 里的路径逐字一致,
--     也与 frontend/src/utils/menu.js 里的 perm 一致。
-- ---------------------------------------------------------------------------

INSERT INTO `sys_permission`
  (`app_id`, `parent_id`, `perm_name`, `perm_key`, `perm_type`,
   `path`, `component`, `icon`, `api_method`, `api_uri`,
   `visible`, `keep_alive`, `is_frame`, `sort_no`, `status`, `sync_source`, `remark`, `creator_id`)
SELECT @app_id, 0, '仪表盘', 'task-system:dashboard', 'M', '/', 'Dashboard', 'Odometer', '', '', 1, 1, 0, 1, 1, 'manual', '概览页', 0
ON DUPLICATE KEY UPDATE `perm_name` = VALUES(`perm_name`), `path` = VALUES(`path`),
  `component` = VALUES(`component`), `icon` = VALUES(`icon`), `update_time` = NOW(3);

INSERT INTO `sys_permission`
  (`app_id`, `parent_id`, `perm_name`, `perm_key`, `perm_type`,
   `path`, `component`, `icon`, `api_method`, `api_uri`,
   `visible`, `keep_alive`, `is_frame`, `sort_no`, `status`, `sync_source`, `remark`, `creator_id`)
SELECT @app_id, 0, '任务管理', 'task-system:task', 'M', '/tasks', 'TaskList', 'Tickets', '', '', 1, 1, 0, 2, 1, 'manual', '任务列表与新建', 0
ON DUPLICATE KEY UPDATE `perm_name` = VALUES(`perm_name`), `path` = VALUES(`path`),
  `component` = VALUES(`component`), `icon` = VALUES(`icon`), `update_time` = NOW(3);

INSERT INTO `sys_permission`
  (`app_id`, `parent_id`, `perm_name`, `perm_key`, `perm_type`,
   `path`, `component`, `icon`, `api_method`, `api_uri`,
   `visible`, `keep_alive`, `is_frame`, `sort_no`, `status`, `sync_source`, `remark`, `creator_id`)
SELECT @app_id, 0, '回收站', 'task-system:task-trash', 'M', '/tasks/deleted', 'TaskTrash', 'Delete', '', '', 1, 1, 0, 3, 1, 'manual', '已删任务的查看与恢复', 0
ON DUPLICATE KEY UPDATE `perm_name` = VALUES(`perm_name`), `path` = VALUES(`path`),
  `component` = VALUES(`component`), `icon` = VALUES(`icon`), `update_time` = NOW(3);

INSERT INTO `sys_permission`
  (`app_id`, `parent_id`, `perm_name`, `perm_key`, `perm_type`,
   `path`, `component`, `icon`, `api_method`, `api_uri`,
   `visible`, `keep_alive`, `is_frame`, `sort_no`, `status`, `sync_source`, `remark`, `creator_id`)
SELECT @app_id, 0, '用户档案', 'task-system:user', 'M', '/users', 'UserManage', 'User', '', '', 1, 1, 0, 4, 1, 'manual', '本地档案列表', 0
ON DUPLICATE KEY UPDATE `perm_name` = VALUES(`perm_name`), `path` = VALUES(`path`),
  `component` = VALUES(`component`), `icon` = VALUES(`icon`), `update_time` = NOW(3);

-- 取出四个菜单的 id, 给下面的按钮挂父级。
-- 挂父级不是装饰: 授权树按 parent_id 建层级, 平铺的码在勾选时看不出"这几个是一组",
-- 而一次勾错的表现是"这个人能删任务但不能看任务"。
SET @menu_dashboard = (SELECT `id` FROM `sys_permission` WHERE `app_id` = @app_id AND `perm_key` = 'task-system:dashboard' AND `del_flag` = 0 LIMIT 1);
SET @menu_task      = (SELECT `id` FROM `sys_permission` WHERE `app_id` = @app_id AND `perm_key` = 'task-system:task'      AND `del_flag` = 0 LIMIT 1);
SET @menu_trash     = (SELECT `id` FROM `sys_permission` WHERE `app_id` = @app_id AND `perm_key` = 'task-system:task-trash' AND `del_flag` = 0 LIMIT 1);
SET @menu_user      = (SELECT `id` FROM `sys_permission` WHERE `app_id` = @app_id AND `perm_key` = 'task-system:user'       AND `del_flag` = 0 LIMIT 1);

-- ---------------------------------------------------------------------------
-- 二、按钮/接口(B)。与 internal/handler/handler.go 顶部那组常量逐字对应。
-- ---------------------------------------------------------------------------

INSERT INTO `sys_permission`
  (`app_id`, `parent_id`, `perm_name`, `perm_key`, `perm_type`,
   `path`, `component`, `icon`, `api_method`, `api_uri`,
   `visible`, `keep_alive`, `is_frame`, `sort_no`, `status`, `sync_source`, `remark`, `creator_id`)
SELECT @app_id, @menu_dashboard, '查看仪表盘', 'task-system:dashboard:view', 'B', '', '', '', 'GET', '/api/v1/stats/overview', 1, 1, 0, 1, 1, 'manual', '', 0
ON DUPLICATE KEY UPDATE `perm_name` = VALUES(`perm_name`), `api_method` = VALUES(`api_method`),
  `api_uri` = VALUES(`api_uri`), `parent_id` = VALUES(`parent_id`), `update_time` = NOW(3);

INSERT INTO `sys_permission`
  (`app_id`, `parent_id`, `perm_name`, `perm_key`, `perm_type`,
   `path`, `component`, `icon`, `api_method`, `api_uri`,
   `visible`, `keep_alive`, `is_frame`, `sort_no`, `status`, `sync_source`, `remark`, `creator_id`)
SELECT @app_id, @menu_task, '查看任务', 'task-system:task:list', 'B', '', '', '', 'GET', '/api/v1/tasks', 1, 1, 0, 1, 1, 'manual', '只看得到与自己相关的任务', 0
ON DUPLICATE KEY UPDATE `perm_name` = VALUES(`perm_name`), `api_method` = VALUES(`api_method`),
  `api_uri` = VALUES(`api_uri`), `parent_id` = VALUES(`parent_id`), `update_time` = NOW(3);

INSERT INTO `sys_permission`
  (`app_id`, `parent_id`, `perm_name`, `perm_key`, `perm_type`,
   `path`, `component`, `icon`, `api_method`, `api_uri`,
   `visible`, `keep_alive`, `is_frame`, `sort_no`, `status`, `sync_source`, `remark`, `creator_id`)
SELECT @app_id, @menu_task, '查看全部任务', 'task-system:task:list-all', 'B', '', '', '', 'GET', '/api/v1/tasks?visibility=all', 1, 1, 0, 2, 1, 'manual', '数据范围开关: 没有它的人只看得到自己创建/被指派/被分享的任务', 0
ON DUPLICATE KEY UPDATE `perm_name` = VALUES(`perm_name`), `api_method` = VALUES(`api_method`),
  `api_uri` = VALUES(`api_uri`), `parent_id` = VALUES(`parent_id`), `update_time` = NOW(3);

INSERT INTO `sys_permission`
  (`app_id`, `parent_id`, `perm_name`, `perm_key`, `perm_type`,
   `path`, `component`, `icon`, `api_method`, `api_uri`,
   `visible`, `keep_alive`, `is_frame`, `sort_no`, `status`, `sync_source`, `remark`, `creator_id`)
SELECT @app_id, @menu_task, '创建任务', 'task-system:task:create', 'B', '', '', '', 'POST', '/api/v1/tasks', 1, 1, 0, 3, 1, 'manual', '', 0
ON DUPLICATE KEY UPDATE `perm_name` = VALUES(`perm_name`), `api_method` = VALUES(`api_method`),
  `api_uri` = VALUES(`api_uri`), `parent_id` = VALUES(`parent_id`), `update_time` = NOW(3);

INSERT INTO `sys_permission`
  (`app_id`, `parent_id`, `perm_name`, `perm_key`, `perm_type`,
   `path`, `component`, `icon`, `api_method`, `api_uri`,
   `visible`, `keep_alive`, `is_frame`, `sort_no`, `status`, `sync_source`, `remark`, `creator_id`)
SELECT @app_id, @menu_task, '编辑任务', 'task-system:task:update', 'B', '', '', '', 'PATCH', '/api/v1/tasks/:id', 1, 1, 0, 4, 1, 'manual', '含状态流转与进展; 数据侧仍要求是创建者或被指派者', 0
ON DUPLICATE KEY UPDATE `perm_name` = VALUES(`perm_name`), `api_method` = VALUES(`api_method`),
  `api_uri` = VALUES(`api_uri`), `parent_id` = VALUES(`parent_id`), `update_time` = NOW(3);

INSERT INTO `sys_permission`
  (`app_id`, `parent_id`, `perm_name`, `perm_key`, `perm_type`,
   `path`, `component`, `icon`, `api_method`, `api_uri`,
   `visible`, `keep_alive`, `is_frame`, `sort_no`, `status`, `sync_source`, `remark`, `creator_id`)
SELECT @app_id, @menu_task, '删除任务', 'task-system:task:delete', 'B', '', '', '', 'DELETE', '/api/v1/tasks/:id', 1, 1, 0, 5, 1, 'manual', '逻辑删除, 可在回收站恢复', 0
ON DUPLICATE KEY UPDATE `perm_name` = VALUES(`perm_name`), `api_method` = VALUES(`api_method`),
  `api_uri` = VALUES(`api_uri`), `parent_id` = VALUES(`parent_id`), `update_time` = NOW(3);

INSERT INTO `sys_permission`
  (`app_id`, `parent_id`, `perm_name`, `perm_key`, `perm_type`,
   `path`, `component`, `icon`, `api_method`, `api_uri`,
   `visible`, `keep_alive`, `is_frame`, `sort_no`, `status`, `sync_source`, `remark`, `creator_id`)
SELECT @app_id, @menu_task, '分享任务', 'task-system:task:share', 'B', '', '', '', 'POST', '/api/v1/tasks/:id/shares', 1, 1, 0, 6, 1, 'manual', '', 0
ON DUPLICATE KEY UPDATE `perm_name` = VALUES(`perm_name`), `api_method` = VALUES(`api_method`),
  `api_uri` = VALUES(`api_uri`), `parent_id` = VALUES(`parent_id`), `update_time` = NOW(3);

INSERT INTO `sys_permission`
  (`app_id`, `parent_id`, `perm_name`, `perm_key`, `perm_type`,
   `path`, `component`, `icon`, `api_method`, `api_uri`,
   `visible`, `keep_alive`, `is_frame`, `sort_no`, `status`, `sync_source`, `remark`, `creator_id`)
SELECT @app_id, @menu_task, '管理提醒', 'task-system:reminder:manage', 'B', '', '', '', 'POST', '/api/v1/reminders', 1, 1, 0, 7, 1, 'manual', '建提醒改的是任务的触发行为, 所以与"看提醒"分开', 0
ON DUPLICATE KEY UPDATE `perm_name` = VALUES(`perm_name`), `api_method` = VALUES(`api_method`),
  `api_uri` = VALUES(`api_uri`), `parent_id` = VALUES(`parent_id`), `update_time` = NOW(3);

INSERT INTO `sys_permission`
  (`app_id`, `parent_id`, `perm_name`, `perm_key`, `perm_type`,
   `path`, `component`, `icon`, `api_method`, `api_uri`,
   `visible`, `keep_alive`, `is_frame`, `sort_no`, `status`, `sync_source`, `remark`, `creator_id`)
SELECT @app_id, @menu_trash, '查看与恢复已删任务', 'task-system:task:restore', 'B', '', '', '', 'POST', '/api/v1/tasks/:id/restore', 1, 1, 0, 1, 1, 'manual', '查看与恢复合成一个码: 能看不能恢复是一种没人要的权力', 0
ON DUPLICATE KEY UPDATE `perm_name` = VALUES(`perm_name`), `api_method` = VALUES(`api_method`),
  `api_uri` = VALUES(`api_uri`), `parent_id` = VALUES(`parent_id`), `update_time` = NOW(3);

INSERT INTO `sys_permission`
  (`app_id`, `parent_id`, `perm_name`, `perm_key`, `perm_type`,
   `path`, `component`, `icon`, `api_method`, `api_uri`,
   `visible`, `keep_alive`, `is_frame`, `sort_no`, `status`, `sync_source`, `remark`, `creator_id`)
SELECT @app_id, @menu_user, '查看用户档案', 'task-system:user:view', 'B', '', '', '', 'GET', '/api/v1/users', 1, 1, 0, 1, 1, 'manual', '指派任务要选人, 所以普通成员也需要它', 0
ON DUPLICATE KEY UPDATE `perm_name` = VALUES(`perm_name`), `api_method` = VALUES(`api_method`),
  `api_uri` = VALUES(`api_uri`), `parent_id` = VALUES(`parent_id`), `update_time` = NOW(3);

INSERT INTO `sys_permission`
  (`app_id`, `parent_id`, `perm_name`, `perm_key`, `perm_type`,
   `path`, `component`, `icon`, `api_method`, `api_uri`,
   `visible`, `keep_alive`, `is_frame`, `sort_no`, `status`, `sync_source`, `remark`, `creator_id`)
SELECT @app_id, @menu_user, '编辑用户档案', 'task-system:user:manage', 'B', '', '', '', 'PATCH', '/api/v1/users/:id', 1, 1, 0, 2, 1, 'manual', '只改本地档案的展示字段与"能否被指派"; 账号与角色在平台上', 0
ON DUPLICATE KEY UPDATE `perm_name` = VALUES(`perm_name`), `api_method` = VALUES(`api_method`),
  `api_uri` = VALUES(`api_uri`), `parent_id` = VALUES(`parent_id`), `update_time` = NOW(3);

-- ---------------------------------------------------------------------------
-- 三、建议的两个角色(可选, 手工执行)。
--
-- 超管不需要它们; 这一节给"要给普通成员和业务管理员各配一套"的部署用。
-- 取消注释前先把 <超管的用户 id> 换成实际值 —— 授给 0 会得到一条指向不存在用户的
-- sys_user_role 行, 而它的表现是"这个角色谁都授不上"。
-- ---------------------------------------------------------------------------

-- INSERT INTO `sys_role` (`app_id`, `role_name`, `role_key`, `sort_no`, `data_scope`, `status`, `del_flag`, `deleted_key`, `creator_id`)
-- VALUES (@app_id, '躬行成员', 'task-system-member', 1, '5', 1, 0, '', 0),
--        (@app_id, '躬行管理员', 'task-system-admin', 2, '1', 1, 0, '', 0);

-- 成员: 能干活, 但只看得到与自己相关的任务。
-- INSERT INTO `sys_role_permission` (`role_id`, `permission_id`)
-- SELECT r.`id`, p.`id` FROM `sys_role` r JOIN `sys_permission` p ON p.`app_id` = @app_id
-- WHERE r.`app_id` = @app_id AND r.`role_key` = 'task-system-member'
--   AND p.`perm_key` IN ('task-system:dashboard', 'task-system:dashboard:view',
--                        'task-system:task', 'task-system:task:list', 'task-system:task:create',
--                        'task-system:task:update', 'task-system:task:delete', 'task-system:task:share',
--                        'task-system:reminder:manage',
--                        'task-system:task-trash', 'task-system:task:restore',
--                        'task-system:user', 'task-system:user:view');

-- 管理员: 在成员之上多一个"查看全部任务"(即数据范围)与用户档案编辑。
-- INSERT INTO `sys_role_permission` (`role_id`, `permission_id`)
-- SELECT r.`id`, p.`id` FROM `sys_role` r JOIN `sys_permission` p ON p.`app_id` = @app_id
-- WHERE r.`app_id` = @app_id AND r.`role_key` = 'task-system-admin';
