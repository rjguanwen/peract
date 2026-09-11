package scheduler

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"taskbackend/internal/config"
	"taskbackend/internal/database"
	"taskbackend/internal/model"
	"taskbackend/internal/service"
)

// 调度器两个扫描动作的回归测试。它们不在任何 HTTP 路径上，
// 一旦静默失守（重复投递、每日重复提醒、积压一次性载入）只会在生产暴露，
// 因此必须在这里用真实数据库钉住。

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{DatabaseURL: "sqlite:///" + filepath.ToSlash(filepath.Join(dir, "sched.db"))}
	db, err := database.Open(cfg)
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatalf("迁移: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	return db
}

func silentNotifier() *service.Notifier {
	cfg := &config.Config{NotifyWorkers: 1, NotifyQueueSize: 4096}
	return service.NewNotifier(cfg)
}

func countSent(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&model.Reminder{}).Where("sent = ? AND sent_at IS NOT NULL", true).Count(&n).Error; err != nil {
		t.Fatalf("统计已发送: %v", err)
	}
	return n
}

func TestCheckRemindersClaimsOnlyDueUnsent(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	user := model.NewUser("u1", "u1@test.local", "pw123456", model.RoleUser, "成员")
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("建用户: %v", err)
	}
	task := model.Task{Title: "任务", Status: model.StatusTodo, Priority: model.PriorityMedium, CreatorID: &user.ID}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("建任务: %v", err)
	}

	// 只有「已到点且未发送」的那条会被处理
	due := model.Reminder{TaskID: task.ID, UserID: &user.ID, RemindAt: now.Add(-time.Minute), RemindType: model.RemindManual}
	if err := db.Create(&due).Error; err != nil {
		t.Fatalf("建到期提醒: %v", err)
	}
	future := model.Reminder{TaskID: task.ID, UserID: &user.ID, RemindAt: now.Add(time.Hour), RemindType: model.RemindManual}
	db.Create(&future)
	claimedAt := now.Add(-time.Hour)
	already := model.Reminder{TaskID: task.ID, UserID: &user.ID, RemindAt: now.Add(-2 * time.Hour), RemindType: model.RemindManual, Sent: true, SentAt: &claimedAt}
	db.Create(&already)

	s := New(db, &config.Config{}, silentNotifier())
	s.checkReminders()

	var processed model.Reminder
	if err := db.First(&processed, due.ID).Error; err != nil {
		t.Fatalf("回读到期提醒: %v", err)
	}
	if !processed.Sent {
		t.Fatal("到期提醒未被处理")
	}
	if processed.SentAt == nil {
		t.Fatal("已抢占但未记录 sent_at，重复投递时无从追溯")
	}
	if got := countSent(t, db); got != 2 {
		t.Fatalf("本轮只应新标 1 条（1 新 + 1 原有已发 = 2），实际 %d", got)
	}

	var back model.Reminder
	if err := db.First(&back, future.ID).Error; err != nil {
		t.Fatalf("回读未到点提醒: %v", err)
	}
	if back.Sent {
		t.Fatal("未到点的提醒被提前判了已发送，用户永远收不到它")
	}

	// 再跑一轮不得重复处理：重启或下一轮扫描都必须是幂等的
	sentAt := *processed.SentAt
	s.checkReminders()
	if got := countSent(t, db); got != 2 {
		t.Fatalf("重复扫描造成二次处理，实际 %d", got)
	}
	var again model.Reminder
	db.First(&again, due.ID)
	if again.SentAt == nil || !again.SentAt.Equal(sentAt) {
		t.Fatalf("重复扫描改写了已处理记录的 sent_at：%v -> %v", sentAt, again.SentAt)
	}
}

func TestCheckRemindersRespectsBatchLimit(t *testing.T) {
	db := newTestDB(t)
	user := model.NewUser("u1", "u1@test.local", "pw123456", model.RoleUser, "成员")
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("建用户: %v", err)
	}
	task := model.Task{Title: "积压任务", Status: model.StatusTodo, Priority: model.PriorityMedium, CreatorID: &user.ID}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("建任务: %v", err)
	}

	// 模拟长时间停机后的积压：一次性载入会撑爆内存
	now := time.Now()
	bulk := make([]model.Reminder, 0, reminderBatchSize*2+50)
	for i := 0; i < reminderBatchSize*2+50; i++ {
		bulk = append(bulk, model.Reminder{
			TaskID: task.ID, UserID: &user.ID,
			RemindAt: now.Add(-time.Duration(i) * time.Second), RemindType: model.RemindManual,
		})
	}
	if err := db.Create(&bulk).Error; err != nil {
		t.Fatalf("批量建提醒: %v", err)
	}

	s := New(db, &config.Config{}, silentNotifier())
	s.checkReminders()
	if got := countSent(t, db); got != reminderBatchSize {
		t.Fatalf("单轮应只处理 %d 条，实际 %d", reminderBatchSize, got)
	}
	s.checkReminders()
	if got := countSent(t, db); got != reminderBatchSize*2 {
		t.Fatalf("第二轮应继续处理一批，累计 %d", got)
	}
}

func TestCheckRemindersDeliversOnceAcrossRuns(t *testing.T) {
	var delivered int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&delivered, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	db := newTestDB(t)
	user := model.NewUser("u1", "u1@test.local", "pw123456", model.RoleUser, "成员")
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("建用户: %v", err)
	}
	task := model.Task{Title: "要通知的任务", Status: model.StatusTodo, Priority: model.PriorityMedium, CreatorID: &user.ID}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("建任务: %v", err)
	}
	db.Create(&model.Reminder{TaskID: task.ID, UserID: &user.ID, RemindAt: time.Now().Add(-time.Minute), RemindType: model.RemindManual})

	cfg := &config.Config{
		NotifyWorkers: 1, NotifyQueueSize: 64,
		NotifyWebhookURL: srv.URL, NotifyWebhookType: "generic",
		WebhookTimeout: 2 * time.Second,
	}
	n := service.NewNotifier(cfg)
	n.Start()
	// Stop 会等队列排空，投递计数才可靠
	defer n.Stop(5 * time.Second)

	s := New(db, cfg, n)
	s.checkReminders()
	s.checkReminders() // 第二条：同一批次不得再次投递

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&delivered) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	// 等一小会儿确认第二轮没有补投
	time.Sleep(200 * time.Millisecond)
	if got := atomic.LoadInt64(&delivered); got != 1 {
		t.Fatalf("同一批提醒投递了 %d 次，应为 1 次", got)
	}
}

func TestCheckOverdueTasksRunsOncePerDayPerTask(t *testing.T) {
	db := newTestDB(t)
	user := model.NewUser("u1", "u1@test.local", "pw123456", model.RoleUser, "成员")
	other := model.NewUser("u2", "u2@test.local", "pw123456", model.RoleUser, "同事")
	if err := db.Create(&[]*model.User{user, other}).Error; err != nil {
		t.Fatalf("建用户: %v", err)
	}
	past := model.NewDateTime(time.Now().Add(-24 * time.Hour))
	later := model.NewDateTime(time.Now().Add(24 * time.Hour))

	overdue := model.Task{Title: "逾期了", Status: model.StatusInProgress, Priority: model.PriorityHigh, AssigneeID: &user.ID, DueDate: past}
	done := model.Task{Title: "已完成", Status: model.StatusDone, Priority: model.PriorityHigh, AssigneeID: &user.ID, DueDate: past}
	unDue := model.Task{Title: "还没到期", Status: model.StatusTodo, Priority: model.PriorityHigh, AssigneeID: &user.ID, DueDate: later}
	noAssignee := model.Task{Title: "逾期但没负责人", Status: model.StatusTodo, Priority: model.PriorityMedium, CreatorID: &other.ID, DueDate: past}
	noDue := model.Task{Title: "没有截止时间", Status: model.StatusTodo, Priority: model.PriorityMedium, AssigneeID: &user.ID}
	if err := db.Create(&[]model.Task{overdue, done, unDue, noAssignee, noDue}).Error; err != nil {
		t.Fatalf("建任务: %v", err)
	}

	s := New(db, &config.Config{}, silentNotifier())
	s.checkOverdueTasks()

	var created []model.Reminder
	if err := db.Where("remind_type = ?", model.RemindOverdue).Order("id ASC").Find(&created).Error; err != nil {
		t.Fatalf("查询逾期提醒: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("应只为两个逾期未完成任务生成提醒，实际 %d 条", len(created))
	}
	// 无负责人任务不能投递给无关第三方
	var nilTarget int
	for _, r := range created {
		if r.UserID == nil {
			nilTarget++
			continue
		}
		if *r.UserID != user.ID {
			t.Fatalf("逾期提醒发给了错的人：%v", *r.UserID)
		}
	}
	if nilTarget != 1 {
		t.Fatalf("无负责人的逾期任务应生成一条不带收件人的提醒，实际 %d", nilTarget)
	}

	// 同日重复扫描不得再来一遍
	s.checkOverdueTasks()
	var total int64
	if err := db.Model(&model.Reminder{}).Where("remind_type = ?", model.RemindOverdue).Count(&total).Error; err != nil {
		t.Fatalf("统计逾期提醒: %v", err)
	}
	if total != 2 {
		t.Fatalf("同日重复扫描又生成了一批，累计 %d 条", total)
	}
}

func TestStopWithoutStartIsSafe(t *testing.T) {
	// main.go 的停机顺序里 Stop 一定被调用，Start 未必成功
	New(newTestDB(t), &config.Config{}, silentNotifier()).Stop()
}
