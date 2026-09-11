package scheduler

import (
	"log"
	"time"

	"github.com/robfig/cron/v3"
	"gorm.io/gorm"

	"taskbackend/internal/config"
	"taskbackend/internal/model"
	"taskbackend/internal/service"
)

// reminderBatchSize 单轮最多投递的提醒条数。
// 长时间停机后可能积压成千上万条到期提醒，一次性载入会撑爆内存并独占数据库连接。
const reminderBatchSize = 100

type Scheduler struct {
	cron   *cron.Cron
	db     *gorm.DB
	cfg    *config.Config
	notify *service.Notifier
}

func New(db *gorm.DB, cfg *config.Config, notify *service.Notifier) *Scheduler {
	return &Scheduler{db: db, cfg: cfg, notify: notify}
}

func (s *Scheduler) Start() {
	// Recover：单个任务 panic 不应带走整个进程；
	// SkipIfStillRunning：一次扫描超过周期时跳过而不是并发重叠，避免同一批提醒被推两次。
	s.cron = cron.New(
		cron.WithSeconds(),
		cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger), cron.Recover(cron.DefaultLogger)),
	)
	// 每 30 秒扫描到期提醒
	if _, err := s.cron.AddFunc("*/30 * * * * *", s.checkReminders); err != nil {
		log.Printf("scheduler: 注册提醒扫描失败: %v", err)
	}
	// 每 10 分钟扫描逾期任务生成提醒
	if _, err := s.cron.AddFunc("0 */10 * * * *", s.checkOverdueTasks); err != nil {
		log.Printf("scheduler: 注册逾期扫描失败: %v", err)
	}
	s.cron.Start()
	log.Println("定时任务已启动")
}

// Stop 等待正在执行的任务收尾，最多 5 秒。
func (s *Scheduler) Stop() {
	if s.cron == nil {
		return
	}
	ctx := s.cron.Stop()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		log.Println("[scheduler] 等待定时任务收尾超时")
	}
}

// checkReminders 到达提醒时间的提醒：标记已发送并投递外部通知
//
// 顺序是先抢占、后投递。反过来（投递完再落库）一旦进程中途崩溃就会在重启后重发；
// 而先落库最多在崩溃时丢掉一条通知，代价小得多。
func (s *Scheduler) checkReminders() {
	now := time.Now()

	var ids []uint
	if err := s.db.Model(&model.Reminder{}).
		Where("sent = ? AND remind_at <= ?", false, now).
		Order("remind_at ASC").Limit(reminderBatchSize).
		Pluck("id", &ids).Error; err != nil {
		log.Printf("[scheduler] 查询到期提醒失败: %v", err)
		return
	}
	if len(ids) == 0 {
		return
	}

	// 抢占：条件带上 sent = false，只有本次真正改写成功的记录才归本实例投递
	claim := s.db.Model(&model.Reminder{}).
		Where("id IN ? AND sent = ?", ids, false).
		UpdateColumns(map[string]any{"sent": true, "sent_at": now})
	if claim.Error != nil {
		log.Printf("[scheduler] 抢占提醒失败: %v", claim.Error)
		return
	}
	if claim.RowsAffected == 0 {
		return
	}

	var due []model.Reminder
	if err := s.db.Where("id IN ? AND sent = ?", ids, true).Find(&due).Error; err != nil {
		log.Printf("[scheduler] 读取已抢占提醒失败: %v", err)
		return
	}

	// 批量补齐任务标题与收件邮箱，避免每条提醒两次查询
	titles := s.taskTitles(due)
	emails := s.userEmails(due)

	for i := range due {
		r := &due[i]
		title := "任务提醒"
		if t := titles[r.TaskID]; t != "" {
			title = "任务提醒：" + t
		}
		msg := r.Message
		if msg == "" {
			msg = title
		}
		emailTo := ""
		if r.UserID != nil {
			emailTo = emails[*r.UserID]
		}
		s.notify.Enqueue(service.TaskNotice{Title: title, Content: msg, EmailTo: emailTo})
	}
	if len(due) >= reminderBatchSize {
		log.Printf("[scheduler] 本轮已处理 %d 条提醒，剩余积压将在下一轮继续", reminderBatchSize)
	}
}

// taskTitles 取这批提醒对应任务的标题。用 Unscoped 是为了回收站里的任务也能显示标题，
// 而不是让通知文案退化成「任务提醒（未知任务）」。
func (s *Scheduler) taskTitles(reminders []model.Reminder) map[uint]string {
	ids := make([]uint, 0, len(reminders))
	for i := range reminders {
		ids = append(ids, reminders[i].TaskID)
	}
	type row struct {
		ID    uint
		Title string
	}
	var rows []row
	out := make(map[uint]string, len(ids))
	if err := s.db.Unscoped().Model(&model.Task{}).Select("id, title").
		Where("id IN ?", ids).Scan(&rows).Error; err != nil {
		log.Printf("[scheduler] 批量读取任务标题失败: %v", err)
		return out
	}
	for _, r := range rows {
		out[r.ID] = r.Title
	}
	return out
}

// userEmails 取这批提醒收件人的邮箱。
func (s *Scheduler) userEmails(reminders []model.Reminder) map[uint]string {
	ids := make([]uint, 0, len(reminders))
	for i := range reminders {
		if reminders[i].UserID != nil {
			ids = append(ids, *reminders[i].UserID)
		}
	}
	out := make(map[uint]string, len(ids))
	if len(ids) == 0 {
		return out
	}
	type row struct {
		ID    uint
		Email string
	}
	var rows []row
	if err := s.db.Model(&model.User{}).Select("id, email").
		Where("id IN ?", ids).Scan(&rows).Error; err != nil {
		log.Printf("[scheduler] 批量读取收件邮箱失败: %v", err)
		return out
	}
	for _, r := range rows {
		out[r.ID] = r.Email
	}
	return out
}

// checkOverdueTasks 逾期未完成任务：每天为负责人生成一条逾期提醒
func (s *Scheduler) checkOverdueTasks() {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

	type overdueTask struct {
		ID         uint
		Title      string
		AssigneeID *uint
	}
	var tasks []overdueTask
	if err := s.db.Model(&model.Task{}).
		Select("id, title, assignee_id").
		Where("status <> ? AND due_date IS NOT NULL AND due_date < ?", model.StatusDone, now).
		Scan(&tasks).Error; err != nil {
		log.Printf("[scheduler] 扫描逾期任务失败: %v", err)
		return
	}
	if len(tasks) == 0 {
		return
	}

	// 原实现对每个任务单独查一次「最近一条逾期提醒」，任务多时是标准的 N+1；
	// 这里改成一次 DISTINCT，直接拿到今天已经提醒过的任务集合。
	taskIDs := make([]uint, 0, len(tasks))
	for _, t := range tasks {
		taskIDs = append(taskIDs, t.ID)
	}
	var notified []uint
	if err := s.db.Model(&model.Reminder{}).
		Where("task_id IN ? AND remind_type = ? AND created_at >= ?", taskIDs, model.RemindOverdue, todayStart).
		Distinct("task_id").Pluck("task_id", &notified).Error; err != nil {
		log.Printf("[scheduler] 查询今日逾期提醒失败: %v", err)
		return
	}
	skipped := make(map[uint]bool, len(notified))
	for _, id := range notified {
		skipped[id] = true
	}

	batch := make([]model.Reminder, 0, len(tasks))
	for _, t := range tasks {
		if skipped[t.ID] {
			continue
		}
		batch = append(batch, model.Reminder{
			TaskID:     t.ID,
			UserID:     t.AssigneeID,
			RemindAt:   now,
			RemindType: model.RemindOverdue,
			Message:    "任务「" + t.Title + "」已逾期，请尽快处理",
		})
	}
	if len(batch) == 0 {
		return
	}
	// 一批提醒一次写入；分批大小由 SQLite 的变量上限约束，这里按 200 条切片
	for start := 0; start < len(batch); start += 200 {
		end := start + 200
		if end > len(batch) {
			end = len(batch)
		}
		if err := s.db.Create(batch[start:end]).Error; err != nil {
			log.Printf("[scheduler] 生成逾期提醒失败: %v", err)
			return
		}
	}
	log.Printf("[scheduler] 新增逾期提醒 %d 条", len(batch))
}
